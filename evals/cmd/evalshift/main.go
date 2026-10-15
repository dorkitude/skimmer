// evalshift measures boundary sensitivity: it re-chunks the corpus with the
// head of each document dropped (shifting every chunk boundary), re-runs the
// eval live, and archives results. Gold for a shifted corpus is a *content*
// match: a kept chunk whose text shares the decisive fact is a hit. Explicit
// invocation only; nothing here runs in CI.
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
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/jev"
	"github.com/dorkitude/skimmer/internal/prompts"
	"github.com/dorkitude/skimmer/internal/skim"
)

type Goal struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Goal         string `json:"goal"`
	Doc          string `json:"doc,omitempty"`
	GoldRaw      any    `json:"gold,omitempty"`
	DecisiveFact string `json:"decisive_fact,omitempty"`
}

type CatalogChunk struct {
	ID   string `json:"id"`
	Doc  string `json:"doc"`
	Text string `json:"text"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "evalshift:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	f := flag.NewFlagSet("evalshift", flag.ContinueOnError)
	model := f.String("model", "", "pinned Jev model ID; required")
	draws := f.Int("draws", 1, "independent draws")
	goalsPath := f.String("goals", "evals/gold/goals.json", "frozen goals JSON")
	catalogPath := f.String("catalog", "evals/gold/catalog.json", "unshifted chunk catalog (for gold fact text)")
	corpusDir := f.String("corpus", "evals/corpus/raw", "raw corpus dir")
	skV := f.String("skimmer-version", "0.1.101-shift", "history folder label")
	threshold := f.Float64("threshold", 0.5, "keep threshold")
	shift := f.Int("shift", 200, "runes dropped from the head of each doc")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *model == "" || strings.Contains(*model, "latest") {
		return fmt.Errorf("-model must be a pinned versioned ID")
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

	files, _ := os.ReadDir(*corpusDir)
	var names []string
	for _, e := range files {
		if strings.HasSuffix(e.Name(), ".txt") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	shiftDir := filepath.Join("evals/gold", fmt.Sprintf("shifted-%d", *shift))
	if err := os.MkdirAll(shiftDir, 0o755); err != nil {
		return err
	}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(*corpusDir, n))
		if err != nil {
			return err
		}
		r := []rune(string(b))
		if len(r) > *shift {
			r = r[*shift:]
		} else {
			r = nil
		}
		if err := os.WriteFile(filepath.Join(shiftDir, n), []byte(string(r)), 0o644); err != nil {
			return err
		}
	}

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
	var sources []skim.Source
	for _, n := range names {
		sources = append(sources, skim.Source{Kind: skim.KindFile, Ref: filepath.Join(shiftDir, n)})
	}
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
	h := sha256.New()
	h.Write(goalBytes)
	fmt.Fprintf(h, "shift:%d", *shift)
	fingerprint := hex.EncodeToString(h.Sum(nil))[:32]

	type gres struct {
		goal Goal
		draw int
		// kept texts carry the chunk text so a hit is a content match
		keptTexts []string
		keptIDs   []string
		tokens    int
		err       string
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
				s := &skim.Skimmer{
					Client: client, Prompt: prompt,
					Goal: g.Goal, Threshold: *threshold,
					ChunkChars: config.DefaultChunkChars, Overlap: config.DefaultOverlap,
					Parallel: 9,
				}
				res, usage := s.Run(ctx, sources)
				gr := gres{goal: g, draw: draw, tokens: usage.InputTokens}
				for _, r := range res {
					if r.Error != "" {
						gr.err = r.Error
					}
					doc := strings.TrimSuffix(filepath.Base(r.Source), ".txt")
					for _, k := range r.Kept {
						gr.keptIDs = append(gr.keptIDs, fmt.Sprintf("%s-%02d", doc, k.Index))
						gr.keptTexts = append(gr.keptTexts, k.Text)
					}
				}
				mu.Lock()
				grs = append(grs, gr)
				mu.Unlock()
			}(g, draw)
		}
	}
	wg.Wait()

	// Gold fact texts from the unshifted catalog.
	goldTexts := map[string][]string{}
	for _, g := range goals {
		if g.Kind != "gold" || g.DecisiveFact == "" {
			continue
		}
		var ids []string
		switch v := g.GoldRaw.(type) {
		case string:
			ids = []string{v}
		case []string:
			ids = v
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					ids = append(ids, s)
				}
			}
		}
		for _, id := range ids {
			for _, c := range catalog {
				if c.ID == id {
					goldTexts[g.ID] = append(goldTexts[g.ID], c.Text)
				}
			}
		}
	}
	// alnum-normalized substring match of the decisive fact in a kept chunk.
	normalize := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	hit := func(g Goal, kept string) bool {
		f := normalize(g.DecisiveFact)
		if len(f) < 16 {
			return false
		}
		t := normalize(kept)
		if strings.Contains(t, f) {
			return true
		}
		for k := 0; k+len(f) <= len(t); k++ {
			if t[k:k+16] == f[:16] {
				return true
			}
		}
		// rolling 16-char probes over the fact
		for k := 0; k+16 <= len(f); k++ {
			if strings.Contains(t, f[k:k+16]) {
				return true
			}
		}
		return false
	}

	type drawStat struct {
		Draw           int     `json:"draw"`
		Gold           int     `json:"gold_goals"`
		GoldKept       int     `json:"gold_kept"` // any kept chunk carries the decisive fact
		Distractors    int     `json:"distractor_goals"`
		DistractorKept int     `json:"distractor_kept_any"`
		KeptMean       float64 `json:"kept_mean_chunks_per_goal"`
		Tokens         int     `json:"input_tokens"`
	}
	stats := map[int]*drawStat{}
	for _, gr := range grs {
		st := stats[gr.draw]
		if st == nil {
			st = &drawStat{Draw: gr.draw}
			stats[gr.draw] = st
		}
		st.Tokens += gr.tokens
		st.KeptMean += float64(len(gr.keptIDs))
		if gr.goal.Kind == "distractor" {
			st.Distractors++
			if len(gr.keptIDs) > 0 {
				st.DistractorKept++
			}
			continue
		}
		st.Gold++
		found := false
		for _, kt := range gr.keptTexts {
			if hit(gr.goal, kt) {
				found = true
				break
			}
		}
		if found {
			st.GoldKept++
		}
	}
	var drawStats []drawStat
	for d := 1; d <= 10; d++ {
		if st, ok := stats[d]; ok {
			st.KeptMean /= float64(st.Gold + st.Distractors)
			drawStats = append(drawStats, *st)
		}
	}

	enc := json.NewEncoder(runsF)
	if err := enc.Encode(map[string]any{"schema": "skimmer-eval/v1", "type": "header", "run_id": runID, "model": *model, "skimmer_version": *skV, "draws": *draws, "threshold": *threshold, "shift": *shift, "fingerprint": fingerprint, "ts": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return err
	}
	sort.Slice(grs, func(a, b int) bool { return grs[a].goal.ID < grs[b].goal.ID })
	for _, gr := range grs {
		found := false
		for _, kt := range gr.keptTexts {
			if hit(gr.goal, kt) {
				found = true
				break
			}
		}
		if err := enc.Encode(map[string]any{"schema": "skimmer-eval/v1", "run_id": runID, "goal": gr.goal.ID, "draw": gr.draw, "kind": gr.goal.Kind, "model": *model, "skimmer_version": *skV, "shift": *shift, "kept_ids": gr.keptIDs, "fact_hit": found, "input_tokens": gr.tokens, "ts": time.Now().UTC().Format(time.RFC3339)}); err != nil {
			return err
		}
	}
	report := map[string]any{"run_id": runID, "model": *model, "skimmer_version": *skV, "shift": *shift, "threshold": *threshold, "fingerprint": fingerprint, "draws": drawStats}
	out, _ := json.MarshalIndent(report, "", " ")
	fmt.Println(string(out))
	_ = utf8.RuneCountInString
	return nil
}
