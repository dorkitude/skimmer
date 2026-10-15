// evalvariants runs technique variants against the goals-v2 eval and reports
// recall@k, all-modes coverage, and token cost per goal. Each variant is a
// different way of asking Jev the same thing; the harness keeps everything
// else fixed (corpus, chunking, gold, draws). Explicitly invoked only.
//
// The variants, in plain English:
//
//	base     - the shipped design: each chunk's text is copied into its own
//	           question, which asks one broad "does this help the goal?" yes/no.
//	state    - content stays in the state; the question just points at the
//	           chunk ("look at item_X") and asks the same yes/no.
//	atomic   - two small questions per chunk ("is it about the right subject?"
//	           and "does it state a specific usable fact for the goal?"),
//	           combined by code: a chunk counts as yes when both answers
//	           pass their thresholds.
//	rerank   - two passes: the base question first to build a shortlist of
//	           the top 10, then a second request that compares the shortlist
//	           members and picks which of them best answers the goal.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dorkitude/skimmer/internal/chunk"
	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/jev"
)

type Goal struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Goal    string   `json:"goal"`
	Doc     string   `json:"doc"`
	Gold    []string `json:"gold"`
	DecFact string   `json:"decisive_fact,omitempty"`
}

type gres struct {
	Goal   Goal               `json:"goal"`
	Draw   int                `json:"draw"`
	Scores map[string]float64 `json:"scores"` // chunk id -> combined probability
	Tokens int                `json:"tokens"`
	Err    string             `json:"err,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "evalvariants:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	f := flag.NewFlagSet("evalvariants", flag.ContinueOnError)
	model := f.String("model", "", "pinned Jev model ID; required")
	variant := f.String("variant", "", "base|state|atomic|rerank; required")
	draws := f.Int("draws", 2, "independent draws")
	goalsPath := f.String("goals", "evals/gold/goals-v2.json", "frozen goals JSON")
	corpusDir := f.String("corpus", "evals/corpus/raw", "raw corpus dir")
	outDir := f.String("out", "evals/variants", "output dir")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *model == "" || strings.Contains(*model, "latest") {
		return fmt.Errorf("-model must be a pinned versioned ID")
	}
	switch *variant {
	case "base", "state", "atomic", "rerank", "collect":
	default:
		return fmt.Errorf("unknown variant %q", *variant)
	}
	goalBytes, err := os.ReadFile(*goalsPath)
	if err != nil {
		return err
	}
	var goals []Goal
	if err := json.Unmarshal(goalBytes, &goals); err != nil {
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

	// Chunk each doc once, deterministically (skimmer defaults).
	type docChunks struct {
		name   string
		doc    string
		chunks []chunk.Chunk
	}
	var docs []docChunks
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(*corpusDir, n))
		if err != nil {
			return err
		}
		docs = append(docs, docChunks{n, strings.TrimSuffix(n, ".txt"), chunk.Split(string(b), config.DefaultChunkChars, int(config.DefaultOverlap*float64(config.DefaultChunkChars)))})
	}

	ctx := context.Background()
	baseURL := os.Getenv("SKIMMER_BASE_URL")
	if baseURL == "" {
		baseURL = config.DefaultBaseURL
	}
	client := jev.NewClient(os.Getenv("SKIMMER_API_KEY"))
	client.BaseURL = baseURL
	client.Model = *model
	client.SetConcurrency(6)

	var (
		mu  sync.Mutex
		grs []gres
		wg  sync.WaitGroup
		sem = make(chan struct{}, 4)
	)
	for draw := 1; draw <= *draws; draw++ {
		for _, g := range goals {
			wg.Add(1)
			go func(g Goal, draw int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				var dc docChunks
				for _, d := range docs {
					if d.doc == g.Doc {
						dc = d
					}
				}
				if dc.doc == "" {
					return
				}
				scores, tokens, err := runVariant(ctx, client, *variant, g, dc.chunks, dc.doc)
				mu.Lock()
				grs = append(grs, gres{Goal: g, Draw: draw, Scores: scores, Tokens: tokens, Err: errStr(err)})
				mu.Unlock()
			}(g, draw)
		}
	}
	wg.Wait()

	// Metrics: recall@k, all-modes@10, MRR (ranked by score), tokens.
	report := metrics(grs)
	report["variant"] = *variant
	report["model"] = *model
	report["draw_count"] = *draws
	out, _ := json.MarshalIndent(report, "", " ")
	fmt.Println(string(out))
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	rp, err := json.Marshal(grs)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*outDir, *variant+"-runs.json"), rp, 0o644)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// runVariant scores every chunk of one doc for one goal, variant by variant.
func runVariant(ctx context.Context, c *jev.Client, variant string, g Goal, chunks []chunk.Chunk, doc string) (map[string]float64, int, error) {
	switch variant {
	case "base":
		// The shipped design: one broad question per chunk, chunk text copied in.
		// "Does this chunk help someone accomplish the goal?"
		state := map[string]any{"goal": g.Goal, "doc": doc}
		questions := map[string]jev.Question{}
		ids := make([]string, len(chunks))
		for i, ch := range chunks {
			id := fmt.Sprintf("c%02d", i)
			ids[i] = id
			state[id] = ch.Text
			questions[id] = jev.NoulQuestion(fmt.Sprintf(
				"The state holds a goal and chunks of one document. Each chunk c00..c%02d holds its own text.\nGoal: %s\n"+
					"Does chunk \"%s\" carry substance that bears on the goal: facts, numbers, definitions, code, steps, decisions, or reasoning the reader would otherwise have to go read? "+
					"Answer no for boilerplate, generic background, passages about a different topic, or chunks that only mention a goal keyword without saying anything useful about it. Judge only this one chunk.",
				len(chunks)-1, g.Goal, id))
		}
		return ask(ctx, c, doc, state, questions, ids)
	case "state":
		// Content stays in the state; each question just points at the chunk.
		state := map[string]any{"goal": g.Goal, "doc": doc}
		questions := map[string]jev.Question{}
		ids := make([]string, len(chunks))
		for i, ch := range chunks {
			id := fmt.Sprintf("c%02d", i)
			ids[i] = id
			state[id] = ch.Text
			questions[id] = jev.NoulQuestion(fmt.Sprintf(
				"The state holds a goal and the chunks of one document, each under its own id (c00..c%02d). Does chunk `%s` help someone accomplish the goal?", len(chunks)-1, id))
		}
		return ask(ctx, c, doc, state, questions, ids)
	case "atomic":
		// Two small questions per chunk, combined by code (both must pass).
		state := map[string]any{"goal": g.Goal, "doc": doc}
		questions := map[string]jev.Question{}
		ids := make([]string, len(chunks))
		for i, ch := range chunks {
			id := fmt.Sprintf("c%02d", i)
			ids[i] = id
			state[id] = ch.Text
			questions[id+"_subject"] = jev.NoulQuestion(fmt.Sprintf(
				"The state holds a goal and the chunks of one document, each under its own id (c00..c%02d). Is chunk `%s` about the subject the goal asks about (same topic, people, system, or material), ignoring whether it goes deep?", len(chunks)-1, id))
			questions[id+"_fact"] = jev.NoulQuestion(fmt.Sprintf(
				"The state holds a goal and the chunks of one document, each under its own id (c00..c%02d). Does chunk `%s` state a specific fact, number, name, date, definition, step, or decision that a reader pursuing the goal would use?", len(chunks)-1, id))
		}
		ps, tokens, err := askQ(ctx, c, state, questions)
		if err != nil {
			return nil, tokens, err
		}
		out := map[string]float64{}
		for _, id := range ids {
			s1, s2 := ps[id+"_subject"], ps[id+"_fact"]
			out[catID(doc, id)] = s1 * s2 // combined in code: both must be high
		}
		return out, tokens, nil
	case "collect":
		// Like "state", but the question names the shape of the goal: a
		// reader collecting every instance of something needs *each*
		// chunk that carries one, not just the best chunk.
		state := map[string]any{"goal": g.Goal, "doc": doc}
		questions := map[string]jev.Question{}
		ids := make([]string, len(chunks))
		for i, ch := range chunks {
			id := fmt.Sprintf("c%02d", i)
			ids[i] = id
			state[id] = ch.Text
			questions[id] = jev.NoulQuestion(fmt.Sprintf(
				"The state holds a goal and the chunks of one document, each under its own id (c00..c%02d). The goal may ask for a single fact, or for EVERY instance of something (every figure, every metric, every event of a kind). "+
					"Does chunk `%s` independently carry one thing the goal asks for — even if other chunks carry other instances of the same thing? Each chunk that carries one instance counts yes on its own.", len(chunks)-1, id))
		}
		return ask(ctx, c, doc, state, questions, ids)
	case "rerank":
		// Pass 1: the base question over every chunk (shortlist of 10).
		base, tokens1, err := runVariant(ctx, c, "base", g, chunks, doc)
		if err != nil {
			return nil, tokens1, err
		}
		type sc struct {
			id string
			p  float64
		}
		var all []sc
		for id, p := range base {
			all = append(all, sc{id, p})
		}
		sort.Slice(all, func(a, b int) bool {
			if all[a].p != all[b].p {
				return all[a].p > all[b].p
			}
			return all[a].id < all[b].id
		})
		if len(all) > 10 {
			all = all[:10]
		}
		// Pass 2: one request, one focused question per shortlisted chunk:
		// "which chunk best answers the goal?" asked about each member.
		state := map[string]any{"goal": g.Goal, "doc": doc}
		questions := map[string]jev.Question{}
		for _, a := range all {
			state[a.id] = chunkText(chunks, a.id)
			questions[a.id+"_best"] = jev.NoulQuestion(fmt.Sprintf(
				"The state holds a goal and the ten chunks most likely to help, each under its own id. Of all the chunks in the state, is chunk `%s` the single best one for accomplishing the goal (most on-topic, most substantive)?", a.id))
		}
		ps2, tokens2, err := askQ(ctx, c, state, questions)
		out := map[string]float64{}
		for id, p := range base {
			out[id] = p * 0.5 // off-shortlist chunks rank below every reranked one
		}
		for _, a := range all {
			// a.id is a catalog id (base returns catalog ids). Pass-2
			// questions are keyed by the chunk's question id, cNN.
			nn := strings.SplitN(a.id, "-", 2)[1]
			out[a.id] = a.p + ps2["c"+nn+"_best"]*0.45 // rerank lifts shortlisted chunks
		}
		return out, tokens1 + tokens2, nil
	}
	return nil, 0, fmt.Errorf("unknown variant %q", variant)
}

func chunkText(chunks []chunk.Chunk, id string) string {
	i, err := strconv.Atoi(strings.TrimPrefix(id, "c"))
	if err != nil || i < 0 || i >= len(chunks) {
		return ""
	}
	return chunks[i].Text
}

// metrics computes recall@k, all-modes@10, MRR, and tokens per goal per draw.
func metrics(grs []gres) map[string]any {
	type rk struct {
		Draw    int     `json:"draw"`
		Goals   int     `json:"goals"`
		RAt1    float64 `json:"recall_at_1"`
		RAt3    float64 `json:"recall_at_3"`
		RAt5    float64 `json:"recall_at_5"`
		RAt10   float64 `json:"recall_at_10"`
		Cover10 float64 `json:"all_modes_in_top10"`
		MRR     float64 `json:"mrr"`
		Tokens  int     `json:"input_tokens_per_goal"`
	}
	stats := map[int]*rk{}
	for _, gr := range grs {
		gold := map[string]bool{}
		for _, x := range gr.Goal.Gold {
			gold[x] = true
		}
		if len(gold) == 0 {
			continue
		}
		st := stats[gr.Draw]
		if st == nil {
			st = &rk{Draw: gr.Draw}
			stats[gr.Draw] = st
		}
		type pc struct {
			id string
			p  float64
		}
		var ps []pc
		for id, p := range gr.Scores {
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
		if len(ranks) == 0 {
			st.Goals++
			st.Tokens += gr.Tokens
			continue
		}
		st.Goals++
		st.Tokens += gr.Tokens
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
		st.MRR += 1 / float64(ranks[0])
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
	var draws []rk
	for d := 1; d <= 10; d++ {
		if st, ok := stats[d]; ok && st.Goals > 0 {
			n := float64(st.Goals)
			st.RAt1 /= n
			st.RAt3 /= n
			st.RAt5 /= n
			st.RAt10 /= n
			st.Cover10 /= n
			st.MRR /= n
			st.Tokens /= st.Goals
			draws = append(draws, *st)
		}
	}
	return map[string]any{"draws": draws}
}

// catID maps a question id (c02) to the catalog chunk id (doc-02).
func catID(doc, qid string) string {
	i, err := strconv.Atoi(strings.TrimPrefix(qid, "c"))
	if err != nil {
		return qid
	}
	return fmt.Sprintf("%s-%02d", doc, i)
}

// ask sends one request: state + one question per chunk, ids c00..cNN.
func ask(ctx context.Context, c *jev.Client, doc string, state map[string]any, questions map[string]jev.Question, ids []string) (map[string]float64, int, error) {
	resp, err := c.SystemOne(ctx, &jev.SystemOneRequest{State: state, Questions: questions})
	if err != nil {
		return nil, 0, err
	}
	out := map[string]float64{}
	for _, id := range ids {
		p := 0.0
		if a, ok := resp.Answers[id]; ok && a.Noul != nil {
			p = *a.Noul
		}
		out[catID(doc, id)] = p // c02 -> arxiv-2608.16621-02 so gold matches
	}
	return out, resp.Usage.InputTokens, nil
}

// askQ sends one request and returns every answer by question key.
func askQ(ctx context.Context, c *jev.Client, state map[string]any, questions map[string]jev.Question) (map[string]float64, int, error) {
	resp, err := c.SystemOne(ctx, &jev.SystemOneRequest{State: state, Questions: questions})
	if err != nil {
		return nil, 0, err
	}
	out := map[string]float64{}
	for k, a := range resp.Answers {
		if a.Noul != nil {
			out[k] = *a.Noul
		}
	}
	return out, resp.Usage.InputTokens, nil
}
