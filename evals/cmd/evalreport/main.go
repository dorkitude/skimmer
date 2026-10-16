// evalreport scores evalskim archives offline and prints markdown tables.
// Nothing here calls Jev.
//
//	evalreport summary -run A.jsonl [-score p|weighted:W|gate:G] [-t 0.5] [-check]
//	evalreport sweep   -run A.jsonl [-score ...] [-half dev]
//	evalreport compare -a A.jsonl -b B.jsonl [-score-a ...] [-t-a ...] [-score-b ...] [-t-b ...] [-half holdout]
//	evalreport injection -run A.jsonl
//
// A goal's outcome in one draw is scored by document span (evals/internal/kit).
// Over draws, a goal counts when it holds in most draws (a tie does not).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/dorkitude/skimmer/evals/internal/kit"
	"github.com/dorkitude/skimmer/internal/chunk"
)

// Archive types mirror evalskim's.
type Chunk struct {
	Doc  string             `json:"doc"`
	I    int                `json:"i"`
	Span *kit.Span          `json:"span"`
	P    *float64           `json:"p"`
	X    map[string]float64 `json:"x"`
	Text string             `json:"text"`
}

type Record struct {
	Goal         string  `json:"goal"`
	Set          string  `json:"set"`
	Kind         string  `json:"kind"`
	Half         string  `json:"half"`
	Draw         int     `json:"draw"`
	Model        string  `json:"model"`
	Docs         int     `json:"docs"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Unjudged     int     `json:"unjudged"`
	Error        string  `json:"error"`
	Chunks       []Chunk `json:"chunks"`
}

type Header struct {
	Run        string   `json:"run"`
	Model      string   `json:"model"`
	Draws      int      `json:"draws"`
	Half       string   `json:"half"`
	Prompt     string   `json:"prompt"`
	Extra      []string `json:"extra"`
	ChunkChars int      `json:"chunk_chars"`
	Overlap    float64  `json:"overlap"`
	Shift      int      `json:"shift"`
	Corpus     string   `json:"corpus"`
	Commit     string   `json:"commit"`
	Finger     string   `json:"fingerprint"`
}

type Run struct {
	Path string
	H    Header
	Recs []Record
}

func loadRun(path string) (*Run, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	r := &Run{Path: path}
	first := true
	for sc.Scan() {
		if first {
			first = false
			if err := json.Unmarshal(sc.Bytes(), &r.H); err != nil {
				return nil, fmt.Errorf("%s: header: %w", path, err)
			}
			continue
		}
		var rec Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		r.Recs = append(r.Recs, rec)
	}
	return r, sc.Err()
}

// Rule turns a chunk's answers into a ranking score and a keep decision.
type Rule struct {
	Mode  string // p, weighted, gate
	Param float64
	T     float64
	Check bool // drop chunks the injection check flags
}

func parseRule(s string, t float64, check bool) Rule {
	r := Rule{Mode: "p", T: t, Check: check}
	if mode, param, ok := strings.Cut(s, ":"); ok {
		r.Mode = mode
		r.Param, _ = strconv.ParseFloat(param, 64)
	}
	return r
}

func (r Rule) String() string {
	s := fmt.Sprintf("P ≥ %.2f", r.T)
	switch r.Mode {
	case "weighted":
		s = fmt.Sprintf("%.1f·subject + %.1f·substance ≥ %.2f", r.Param, 1-r.Param, r.T)
	case "gate":
		s = fmt.Sprintf("subject ≥ %.2f and substance ≥ %.2f", r.Param, r.T)
	}
	if r.Check {
		s += ", injection check on"
	}
	return s
}

// score ranks chunks; unjudged chunks rank last.
func (r Rule) score(c Chunk) float64 {
	if c.P == nil {
		return -1
	}
	s, sub := *c.P, c.X["substance"]
	switch r.Mode {
	case "weighted":
		return r.Param*s + (1-r.Param)*sub
	case "gate":
		if s >= r.Param {
			return 0.5 + sub/2
		}
		return sub / 2
	}
	return s
}

// keep: unjudged chunks are always kept, as skimmer does.
func (r Rule) keep(c Chunk) bool {
	if c.P == nil {
		return true
	}
	if r.Check && c.X["injection"] >= 0.5 {
		return false
	}
	switch r.Mode {
	case "gate":
		return *c.P >= r.Param && c.X["substance"] >= r.T
	}
	return r.score(c) >= r.T
}

// Env holds the goals and catalog regions.
type Env struct {
	Goals map[string]kit.Goal
	Cat   kit.Catalog
}

func loadEnv() (*Env, error) {
	goals, err := kit.LoadGoals([]string{"v1", "v2", "nearmiss"})
	if err != nil {
		return nil, err
	}
	cat, err := kit.LoadCatalog()
	if err != nil {
		return nil, err
	}
	e := &Env{Goals: map[string]kit.Goal{}, Cat: cat}
	for _, g := range goals {
		e.Goals[g.ID] = g
	}
	return e, nil
}

// Outcome is one goal in one draw.
type Outcome struct {
	Complete  bool // positive goals
	OtherKept bool // confusables: the near-duplicate's region was kept
	Leak      bool // negative goals: anything kept
	Kept      int
	KeptChars int   // non-whitespace characters in the kept chunks: what the reader pays
	Ranks     []int // per gold region: rank of the first chunk that keeps it alone (0: none)
}

func (e *Env) outcome(rec Record, rule Rule) Outcome {
	g := e.Goals[rec.Goal]
	var o Outcome
	var kept []kit.Span
	for _, c := range rec.Chunks {
		if rule.keep(c) {
			o.Kept++
			if c.Span != nil {
				o.KeptChars += c.Span.Len()
			}
			if c.Span != nil && c.Doc == g.Doc {
				kept = append(kept, *c.Span)
			}
		}
	}
	if g.Negative() {
		o.Leak = o.Kept > 0
		return o
	}
	gold, other := e.Cat.Regions(g)
	hits := 0
	for _, r := range gold {
		if r.Hit(kept) {
			hits++
		}
	}
	switch g.Set {
	case "v2":
		o.Complete = hits == len(gold)
	default:
		o.Complete = hits > 0
	}
	if other != nil {
		o.OtherKept = other.Hit(kept)
	}
	ranked := append([]Chunk{}, rec.Chunks...)
	sort.SliceStable(ranked, func(a, b int) bool {
		sa, sb := rule.score(ranked[a]), rule.score(ranked[b])
		if sa != sb {
			return sa > sb
		}
		if ranked[a].Doc != ranked[b].Doc {
			return ranked[a].Doc < ranked[b].Doc
		}
		return ranked[a].I < ranked[b].I
	})
	o.Ranks = make([]int, len(gold))
	for gi, r := range gold {
		for k, c := range ranked {
			if c.Span != nil && c.Doc == g.Doc && r.HitBy(*c.Span) {
				o.Ranks[gi] = k + 1
				break
			}
		}
	}
	return o
}

// group names the row a goal is reported in.
func group(g kit.Goal) string {
	switch g.Set {
	case "v2":
		return "v2 " + g.Kind
	case "nearmiss":
		return "near-miss"
	}
	return g.Kind
}

var groupOrder = []string{"gold", "confusable", "v2 single", "v2 collect", "v2 comparison", "near-miss", "distractor"}

// byGoal collects a run's outcomes per goal, draws in order.
type goalOutcomes struct {
	Goal  kit.Goal
	Half  string
	Outs  []Outcome
	Recs  []Record
	Draws int
}

func (e *Env) collect(run *Run, rule Rule, half string) map[string]*goalOutcomes {
	out := map[string]*goalOutcomes{}
	for _, rec := range run.Recs {
		if half != "" && half != "all" && rec.Half != half {
			continue
		}
		g, ok := e.Goals[rec.Goal]
		if !ok {
			continue
		}
		gol := out[rec.Goal]
		if gol == nil {
			gol = &goalOutcomes{Goal: g, Half: rec.Half}
			out[rec.Goal] = gol
		}
		gol.Outs = append(gol.Outs, e.outcome(rec, rule))
		gol.Recs = append(gol.Recs, rec)
		gol.Draws++
	}
	return out
}

// majority: true in more than half the draws.
func majority(outs []Outcome, f func(Outcome) bool) bool {
	n := 0
	for _, o := range outs {
		if f(o) {
			n++
		}
	}
	return 2*n > len(outs)
}

func isComplete(o Outcome) bool { return o.Complete }
func isLeak(o Outcome) bool     { return o.Leak }
func isOther(o Outcome) bool    { return o.OtherKept }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: evalreport summary|sweep|compare|injection ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "summary":
		err = cmdSummary(os.Args[2:])
	case "sweep":
		err = cmdSweep(os.Args[2:])
	case "compare":
		err = cmdCompare(os.Args[2:])
	case "injection":
		err = cmdInjection(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "evalreport:", err)
		os.Exit(1)
	}
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func sortedGoals(m map[string]*goalOutcomes) []*goalOutcomes {
	var out []*goalOutcomes
	for _, g := range m {
		out = append(out, g)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Goal.ID < out[b].Goal.ID })
	return out
}

// chunksOf re-chunks a record's documents exactly as the run did, so the
// keyword baseline sees the same chunks.
func chunksOf(run *Run, doc string) ([]string, error) {
	text, err := kit.LoadDoc(run.H.Corpus, doc)
	if err != nil {
		return nil, err
	}
	text = kit.Shift(text, run.H.Shift)
	cs := chunk.Split(text, run.H.ChunkChars, int(run.H.Overlap*float64(run.H.ChunkChars)))
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Text
	}
	return out, nil
}
