// evalskim runs the skimmer eval: for each goal in evals/gold/goals.json,
// it runs the REAL skimmer pipeline (internal/skim) over the eval corpus
// documents against the pinned Jev model, archives every outcome to the
// versioned runs.jsonl, and prints per-draw metrics. Explicitly invoked;
// nothing here runs in CI or go test.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/jev"
	"github.com/dorkitude/skimmer/internal/prompts"
	"github.com/dorkitude/skimmer/internal/skim"
)

// Goal is one frozen eval goal. Gold is a catalog chunk id, or a list for
// multi-gold (the decisive fact spans adjacent chunks).
type Goal struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Goal         string `json:"goal"`
	Doc          string `json:"doc,omitempty"`
	GoldRaw      any    `json:"gold,omitempty"`
	DecisiveFact string `json:"decisive_fact,omitempty"`
	Multi        bool   `json:"multi,omitempty"`
	Other        string `json:"other,omitempty"` // confusable near-duplicate chunk
}

// CatalogChunk mirrors evals/gold/catalog.json.
type CatalogChunk struct {
	ID        string `json:"id"`
	Doc       string `json:"doc"`
	Text      string `json:"text"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type gres struct {
	goal      Goal
	draw      int
	keptIDs   []string
	keptP     map[string]float64
	keptTexts []string
	tokens    int
	nJudged   int // chunks judged (all chunks of the goal's source set)
	err       string
}

func goldIDs(g Goal) []string {
	switch v := g.GoldRaw.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "evalskim:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	f := flag.NewFlagSet("evalskim", flag.ContinueOnError)
	model := f.String("model", "", "pinned Jev model ID (e.g. jev-1.14.0); required")
	draws := f.Int("draws", 2, "independent draws")
	goalsPath := f.String("goals", "evals/gold/goals.json", "frozen goals JSON")
	catalogPath := f.String("catalog", "evals/gold/catalog.json", "chunk catalog JSON")
	corpusDir := f.String("corpus", "evals/corpus/raw", "raw corpus dir")
	skV := f.String("skimmer-version", "0.1.101", "skimmer design version label for the history folder")
	threshold := f.Float64("threshold", 0.5, "keep threshold")
	singleDoc := f.Bool("single-doc", true, "judge each goal over only its own document (skimmer's primary single-source mode)")
	factMatch := f.Bool("fact-match", false, "score gold goals by decisive-fact content match in kept chunks (required when chunk size differs from the catalog)")
	ranked := f.Bool("ranked", false, "v2 goal set: rank-based metrics, recall@k and all-modes coverage")
	chunkChars := f.Int("chunk-chars", 0, "chunk size override (0 = skimmer default)")
	overlap := f.Float64("overlap", -1, "chunk overlap fraction override (-1 = skimmer default)")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *model == "" || strings.Contains(*model, "latest") {
		return fmt.Errorf("-model must be a pinned versioned ID, e.g. jev-1.14.0")
	}

	goalBytes, err := os.ReadFile(*goalsPath)
	if err != nil {
		return err
	}
	var goals []Goal
	if err := json.Unmarshal(goalBytes, &goals); err != nil {
		return err
	}
	catBytes, err := os.ReadFile(*catalogPath)
	if err != nil {
		return err
	}
	var catalog []CatalogChunk
	if err := json.Unmarshal(catBytes, &catalog); err != nil {
		return err
	}

	// Fingerprint over goals + catalog + corpus bytes.
	h := sha256.New()
	h.Write(goalBytes)
	h.Write(catBytes)
	files, _ := os.ReadDir(*corpusDir)
	var names []string
	for _, e := range files {
		if strings.HasSuffix(e.Name(), ".txt") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		b, _ := os.ReadFile(filepath.Join(*corpusDir, n))
		fmt.Fprintf(h, "%d:%s:%d\n", len(n), n, len(b))
		h.Write(b)
	}
	fingerprint := hex.EncodeToString(h.Sum(nil))[:32]

	outDir := filepath.Join("evals/skimmer", *skV)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	runsF, err := os.OpenFile(filepath.Join(outDir, "runs.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer runsF.Close()
	runID := time.Now().UTC().Format("20060102T150405.000000000Z")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	baseURL := os.Getenv("SKIMMER_BASE_URL")
	if baseURL == "" {
		baseURL = config.DefaultBaseURL
	}
	client := jev.NewClient(os.Getenv("SKIMMER_API_KEY"))
	client.BaseURL = baseURL
	client.Model = *model
	client.SetConcurrency(6)

	prompt, err := prompts.Load(prompts.SkimDocument, "", "")
	if err != nil {
		return err
	}

	sourcesByName := map[string]skim.Source{}
	for _, n := range names {
		sourcesByName[n] = skim.Source{Kind: skim.KindFile, Ref: filepath.Join(*corpusDir, n)}
	}

	var allSources []skim.Source
	for _, n := range names {
		allSources = append(allSources, sourcesByName[n])
	}

	var (
		mu  sync.Mutex
		grs []gres
		wg  sync.WaitGroup
		sem = make(chan struct{}, 3)
	)
	for draw := 1; draw <= *draws; draw++ {
		for _, g := range goals {
			wg.Add(1)
			go func(g Goal, draw int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				goalSources := allSources
				if *singleDoc && g.Doc != "" {
					// single-source mode: one goal over its own document.
					// Distractor goals (no doc) run over every document:
					// "nothing here helps with the goal" is the claim under test.
					goalSources = []skim.Source{sourcesByName[g.Doc+".txt"]}
				}
				cc, ov := *chunkChars, *overlap
				if cc <= 0 {
					cc = config.DefaultChunkChars
				}
				if ov < 0 {
					ov = config.DefaultOverlap
				}
				s := &skim.Skimmer{
					Client: client, Prompt: prompt,
					Goal: g.Goal, Threshold: *threshold,
					ChunkChars: cc, Overlap: ov,
					MaxChars: 0, Parallel: len(goalSources),
				}
				res, usage := s.Run(ctx, goalSources)
				gr := gres{goal: g, draw: draw, keptP: map[string]float64{}, tokens: usage.InputTokens}
				for _, r := range res {
					if r.Error != "" {
						gr.err = r.Error
					}
					gr.nJudged += r.Chunks
					doc := strings.TrimSuffix(filepath.Base(r.Source), ".txt")
					for _, k := range r.Kept {
						id := fmt.Sprintf("%s-%02d", doc, k.Index)
						gr.keptIDs = append(gr.keptIDs, id)
						gr.keptTexts = append(gr.keptTexts, k.Text)
						if k.P != nil {
							gr.keptP[id] = *k.P
						}
					}
				}
				mu.Lock()
				grs = append(grs, gr)
				mu.Unlock()
			}(g, draw)
		}
	}
	wg.Wait()

	// Archive.
	enc := json.NewEncoder(runsF)
	write := func(v any) error { return enc.Encode(v) }
	if err := write(map[string]any{"schema": "skimmer-eval/v1", "type": "header", "run_id": runID, "model": *model, "skimmer_version": *skV, "draws": *draws, "threshold": *threshold, "fingerprint": fingerprint, "ts": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return err
	}
	sort.Slice(grs, func(a, b int) bool {
		if grs[a].draw != grs[b].draw {
			return grs[a].draw < grs[b].draw
		}
		return grs[a].goal.ID < grs[b].goal.ID
	})
	for _, gr := range grs {
		kept := map[string]float64{}
		for id, p := range gr.keptP {
			kept[id] = p
		}
		rec := map[string]any{
			"schema": "skimmer-eval/v1", "run_id": runID, "goal": gr.goal.ID,
			"draw": gr.draw, "kind": gr.goal.Kind, "model": *model,
			"skimmer_version": *skV, "threshold": *threshold,
			"kept": kept, "kept_texts": gr.keptTexts, "input_tokens": gr.tokens, "ts": time.Now().UTC().Format(time.RFC3339),
		}
		if gr.err != "" {
			rec["error"] = gr.err
		}
		if err := write(rec); err != nil {
			return err
		}
	}

	// Metrics.
	report := compute(grs, goals, fingerprint, *model, *skV, *threshold, runID, names, catalog, *factMatch)
	if *ranked {
		report["ranked"] = rankedMetrics(grs)
	}
	out, _ := json.MarshalIndent(report, "", " ")
	fmt.Println(string(out))
	return nil
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func factHit(g Goal, kept string) bool {
	f := normalize(g.DecisiveFact)
	if len(f) < 16 {
		return false
	}
	t := normalize(kept)
	if strings.Contains(t, f) {
		return true
	}
	for k := 0; k+16 <= len(f); k++ {
		if strings.Contains(t, f[k:k+16]) {
			return true
		}
	}
	return false
}

func compute(grs []gres, goals []Goal, fingerprint, model, skV string, threshold float64, runID string, names []string, catalog []CatalogChunk, factMatch bool) map[string]any {
	byID := map[string]Goal{}
	for _, g := range goals {
		byID[g.ID] = g
	}
	type drawStat struct {
		Draw                int     `json:"draw"`
		Gold                int     `json:"gold_goals"`
		GoldKept            int     `json:"gold_kept"` // gold goal with any gold chunk kept
		Confusables         int     `json:"confusable_goals"`
		ConfusableKeptGold  int     `json:"confusable_kept_gold"`  // gold chunk kept
		ConfusableKeptOther int     `json:"confusable_kept_other"` // near-duplicate other chunk kept
		DistractorGoals     int     `json:"distractor_goals"`
		DistractorKeptAny   int     `json:"distractor_kept_any"` // distractor goal keeping any chunk
		KeptMean            float64 `json:"kept_mean_chunks_per_goal"`
		TokensPerGoal       float64 `json:"input_tokens_per_goal"`
		Chunks              float64 `json:"chunks_judged_mean"` // mean chunks judged per goal
	}
	stats := map[int]*drawStat{}
	for _, gr := range grs {
		st := stats[gr.draw]
		if st == nil {
			st = &drawStat{Draw: gr.draw}
			stats[gr.draw] = st
		}
		st.TokensPerGoal += float64(gr.tokens)
		st.Chunks += float64(gr.nJudged)
		st.KeptMean += float64(len(gr.keptIDs))
		if gr.goal.Kind == "distractor" {
			st.DistractorGoals++
			if len(gr.keptIDs) > 0 {
				st.DistractorKeptAny++
			}
			continue
		}
		gold := map[string]bool{}
		for _, id := range goldIDs(gr.goal) {
			gold[id] = true
		}
		if gr.goal.Kind == "confusable" {
			st.Confusables++
			for _, id := range gr.keptIDs {
				if gold[id] {
					st.ConfusableKeptGold++
				}
				if gr.goal.Other != "" && id == gr.goal.Other {
					st.ConfusableKeptOther++
				}
			}
			continue
		}
		st.Gold++
		if factMatch {
			for _, kt := range gr.keptTexts {
				if factHit(gr.goal, kt) {
					st.GoldKept++
					break
				}
			}
			continue
		}
		for _, id := range gr.keptIDs {
			if gold[id] {
				st.GoldKept++
				break
			}
		}
	}
	var draws []drawStat
	for d := 1; d <= 10; d++ {
		if st, ok := stats[d]; ok {
			n := st.Gold + st.DistractorGoals + st.Confusables
			if n > 0 {
				st.KeptMean /= float64(n)
				st.TokensPerGoal /= float64(n)
				st.Chunks /= float64(n)
			}
			draws = append(draws, *st)
		}
	}
	return map[string]any{
		"run_id": runID, "fingerprint": fingerprint, "model": model,
		"skimmer_version": skV, "threshold": threshold,
		"sources": len(names), "catalog_chunks": len(catalog),
		"draws": draws,
	}
}

var _ = strconv.Itoa

// rankedMetrics computes recall@k and all-modes coverage from the archived
// per-chunk probabilities. For each goal, chunks of its own document are
// ranked by kept probability (ties broken by chunk id); recall@k is the share
// of gold chunks within the top k; coverage is the fraction of goals whose
// top-10 contains every gold chunk (mode drop: any gold region missing).
func rankedMetrics(grs []gres) map[string]any {
	type rk struct {
		Draw    int     `json:"draw"`
		Goals   int     `json:"goals"`
		RAt1    float64 `json:"recall_at_1"`
		RAt3    float64 `json:"recall_at_3"`
		RAt5    float64 `json:"recall_at_5"`
		RAt10   float64 `json:"recall_at_10"`
		Cover10 float64 `json:"all_modes_in_top10"`
		MRR     float64 `json:"mrr"`
	}
	stats := map[int]*rk{}
	for _, gr := range grs {
		gold := map[string]bool{}
		switch v := gr.goal.GoldRaw.(type) {
		case string:
			gold[v] = true
		case []string:
			for _, x := range v {
				gold[x] = true
			}
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					gold[s] = true
				}
			}
		}
		if len(gold) == 0 {
			continue
		}
		st := stats[gr.draw]
		if st == nil {
			st = &rk{Draw: gr.draw}
			stats[gr.draw] = st
		}
		type pc struct {
			id string
			p  float64
		}
		var ps []pc
		for id, p := range gr.keptP {
			ps = append(ps, pc{id, p})
		}
		sort.Slice(ps, func(a, b int) bool {
			if ps[a].p != ps[b].p {
				return ps[a].p > ps[b].p
			}
			return ps[a].id < ps[b].id
		})
		var ranks []int
		for k, c := range ps {
			if gold[c.id] {
				ranks = append(ranks, k+1)
			}
		}
		st.Goals++
		in := func(k int) bool {
			for _, r := range ranks {
				if r <= k {
					return true
				}
			}
			return false
		}
		if in(1) {
			st.RAt1++
		}
		if in(3) {
			st.RAt3++
		}
		if in(5) {
			st.RAt5++
		}
		if in(10) {
			st.RAt10++
		}
		if r := ranks[0]; r > 0 {
			st.MRR += 1 / float64(r)
		}
		all := true
		for _, r := range ranks {
			if r > 10 {
				all = false
			}
		}
		if all {
			st.Cover10++
		}
	}
	var out []rk
	for d := 1; d <= 10; d++ {
		if st, ok := stats[d]; ok && st.Goals > 0 {
			n := float64(st.Goals)
			st.RAt1 /= n
			st.RAt3 /= n
			st.RAt5 /= n
			st.RAt10 /= n
			st.Cover10 /= n
			st.MRR /= n
			out = append(out, *st)
		}
	}
	return map[string]any{"draws": out}
}
