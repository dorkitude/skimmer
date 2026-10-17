// evalskim runs skimmer's live eval. For each goal and draw it runs the real
// skim pipeline (internal/skim: chunking, batching, the prompt, the source
// as context) over the goal's document at threshold 0, and archives every
// chunk's span and probability, one JSON line per goal and draw. Nothing is
// scored here: evalreport scores the archive offline, so thresholds,
// combinations and baselines can be compared on identical data.
//
// Explicitly invoked only; nothing here runs in CI or go test. See
// evals/README.md for the commands behind each results table.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dorkitude/skimmer/evals/internal/kit"
	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/jev"
	"github.com/dorkitude/skimmer/internal/prompts"
	"github.com/dorkitude/skimmer/internal/skim"
)

// Chunk is one judged chunk in the archive.
type Chunk struct {
	Doc  string             `json:"doc"`
	I    int                `json:"i"`
	Span *kit.Span          `json:"span,omitempty"` // in the unshifted document; nil if not found
	P    *float64           `json:"p"`
	X    map[string]float64 `json:"x,omitempty"` // extra questions' P(yes)
	Text string             `json:"text,omitempty"`
}

// Record is one goal and draw.
type Record struct {
	Schema       string  `json:"schema"`
	Run          string  `json:"run"`
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
	Error        string  `json:"error,omitempty"`
	Chunks       []Chunk `json:"chunks"`
}

// Header describes the run; it is the first line of the archive.
type Header struct {
	Schema         string   `json:"schema"`
	Type           string   `json:"type"`
	Run            string   `json:"run"`
	Model          string   `json:"model"`
	Draws          int      `json:"draws"`
	Sets           []string `json:"sets"`
	Half           string   `json:"half"`
	Prompt         string   `json:"prompt"`
	Extra          []string `json:"extra,omitempty"`
	ChunkChars     int      `json:"chunk_chars"`
	Overlap        float64  `json:"overlap"`
	Shift          int      `json:"shift"`
	Corpus         string   `json:"corpus"`
	SkimmerVersion string   `json:"skimmer_version"`
	Commit         string   `json:"commit"`
	Fingerprint    string   `json:"fingerprint"`
	Started        string   `json:"started"`
}

const schema = "skimmer-eval/v2"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "evalskim:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	f := flag.NewFlagSet("evalskim", flag.ContinueOnError)
	model := f.String("model", "", "pinned Jev model ID, e.g. jev-1.14.0 (required; aliases are refused)")
	draws := f.Int("draws", 2, "independent draws")
	sets := f.String("sets", "v1,v2,nearmiss", "goal sets: v1, v2, nearmiss")
	half := f.String("half", "dev", "dev, holdout, or all (evals/gold/split.json)")
	kinds := f.String("kinds", "", "only these goal kinds, comma separated (default all)")
	promptPath := f.String("prompt", "", "skim prompt file (default: the built-in skim_document/DEFAULT)")
	extra := f.String("extra", "", "extra questions per chunk, name=prompt.md,... (injection = the built-in check)")
	chunkChars := f.Int("chunk-chars", config.DefaultChunkChars, "chunk size")
	overlap := f.Float64("overlap", config.DefaultOverlap, "chunk overlap fraction")
	shift := f.Int("shift", 0, "drop this many runes from the head of every document, moving every chunk boundary")
	corpus := f.String("corpus", kit.CorpusDir, "corpus directory")
	keepText := f.Bool("keep-text", false, "archive chunk text too")
	out := f.String("out", "", "archive path, e.g. evals/skimmer/0.1.103/runs/dev-shipped.jsonl (required)")
	label := f.String("label", "", "run label (default: the archive's file name)")
	version := f.String("skimmer-version", "0.1.103", "skimmer version under test")
	conc := f.Int("concurrency", 6, "parallel Jev requests")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *model == "" || strings.Contains(*model, "latest") || strings.Contains(*model, "preview") {
		return fmt.Errorf("-model must be a pinned versioned ID, e.g. jev-1.14.0")
	}
	if *out == "" {
		return fmt.Errorf("-out is required")
	}
	if *label == "" {
		*label = strings.TrimSuffix(filepath.Base(*out), ".jsonl")
	}

	split, err := kit.LoadSplit()
	if err != nil {
		return err
	}
	goals, err := kit.LoadGoals(strings.Split(*sets, ","))
	if err != nil {
		return err
	}
	var keep []kit.Goal
	for _, g := range goals {
		if (*half == "all" || split.Of(g) == *half) && (*kinds == "" || inList(*kinds, g.Kind)) {
			keep = append(keep, g)
		}
	}
	goals = keep
	if len(goals) == 0 {
		return fmt.Errorf("no goals selected")
	}

	prompt, err := loadPrompt(prompts.SkimDocument, *promptPath)
	if err != nil {
		return err
	}
	var extras []skim.Question
	var extraNames []string
	if *extra != "" {
		for _, e := range strings.Split(*extra, ",") {
			name, path, _ := strings.Cut(e, "=")
			kind := prompts.SkimDocument
			if name == "injection" {
				kind = prompts.InjectionCheck
			}
			p, err := loadPrompt(kind, path)
			if err != nil {
				return err
			}
			extras = append(extras, skim.Question{Name: name, Prompt: p})
			extraNames = append(extraNames, e)
		}
	}

	// Keep the unshifted input (possibly with planted text) and the original
	// document for mapping spans. Apply the shift only when building sources,
	// so an insertion remains mappable even when combined with a shift.
	docs := map[string]string{}
	locText := map[string]string{}
	for _, d := range split.Docs("all") {
		text, err := kit.LoadDoc(*corpus, d)
		if err != nil {
			return err
		}
		docs[d] = text
		if locText[d], err = kit.LoadDoc(kit.CorpusDir, d); err != nil {
			return err
		}
		if _, err := kit.NewMappedLocator(locText[d], docs[d]); err != nil {
			return fmt.Errorf("map document %s: %w", d, err)
		}
	}

	header := Header{
		Schema: schema, Type: "header", Run: *label, Model: *model, Draws: *draws,
		Sets: strings.Split(*sets, ","), Half: *half, Prompt: promptName(*promptPath), Extra: extraNames,
		ChunkChars: *chunkChars, Overlap: *overlap, Shift: *shift, Corpus: *corpus,
		SkimmerVersion: *version, Commit: commit(),
		Fingerprint: fingerprint(*corpus, split.Docs("all"), *sets, *promptPath, extras),
		Started:     time.Now().UTC().Format(time.RFC3339),
	}

	client := jev.NewClient(apiKey())
	client.BaseURL = os.Getenv("SKIMMER_BASE_URL")
	client.Model = *model
	client.SetConcurrency(*conc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var (
		mu      sync.Mutex
		records []Record
		wg      sync.WaitGroup
		sem     = make(chan struct{}, 4)
		done    int
	)
	total := len(goals) * *draws
	for draw := 1; draw <= *draws; draw++ {
		for _, g := range goals {
			wg.Add(1)
			go func(g kit.Goal, draw int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				var names []string
				if g.Doc != "" {
					names = []string{g.Doc}
				} else {
					names = split.Docs(split.Of(g)) // a distractor: every document of its half
					if *half == "all" {
						names = split.Docs("all")
					}
				}
				rec := skimGoal(ctx, client, prompt, extras, g, names, docs, locText, *corpus, *chunkChars, *overlap, *shift, *keepText)
				rec.Schema, rec.Run, rec.Draw, rec.Half = schema, *label, draw, split.Of(g)
				mu.Lock()
				records = append(records, rec)
				done++
				if done%25 == 0 || done == total {
					fmt.Fprintf(os.Stderr, "evalskim: %s %d/%d\n", *label, done, total)
				}
				mu.Unlock()
			}(g, draw)
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}

	sort.Slice(records, func(a, b int) bool {
		if records[a].Draw != records[b].Draw {
			return records[a].Draw < records[b].Draw
		}
		return records[a].Goal < records[b].Goal
	})
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	file, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	if err := enc.Encode(header); err != nil {
		return err
	}
	var tokens, unjudged, failed int
	for _, r := range records {
		tokens += r.InputTokens
		unjudged += r.Unjudged
		if r.Error != "" {
			failed++
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "evalskim: wrote %s: %d goal-draws, %d input tokens, %d unjudged chunks, %d failed\n",
		*out, len(records), tokens, unjudged, failed)
	return nil
}

func skimGoal(ctx context.Context, client *jev.Client, prompt *prompts.Prompt, extras []skim.Question, g kit.Goal,
	names []string, docs, locText map[string]string, corpus string, chunkChars int, overlap float64, shift int, keepText bool) Record {
	rec := Record{Goal: g.ID, Set: g.Set, Kind: g.Kind, Docs: len(names), Chunks: []Chunk{}}
	sources := make([]skim.Source, len(names))
	for i, n := range names {
		sources[i] = skim.Source{Kind: skim.KindFile, Ref: filepath.Join(corpus, n+".txt"), Text: kit.Shift(docs[n], shift)}
	}
	s := &skim.Skimmer{
		Client: client, Prompt: prompt, Extra: extras, Goal: g.Goal,
		Threshold: 0, ChunkChars: chunkChars, Overlap: overlap, Parallel: len(sources),
	}
	results, usage := s.Run(ctx, sources)
	rec.Model, rec.InputTokens, rec.OutputTokens = usage.Model, usage.InputTokens, usage.OutputTokens
	for i, r := range results {
		if r.Error != "" {
			rec.Error = r.Error
		}
		rec.Unjudged += r.Unjudged
		loc, err := kit.NewMappedLocator(locText[names[i]], docs[names[i]])
		if err != nil {
			rec.Error = fmt.Sprintf("map document %s: %v", names[i], err)
			continue
		}
		for _, k := range r.Kept {
			c := Chunk{Doc: names[i], I: k.Index, P: k.P}
			if sp, ok := loc.Locate(k.Text); ok {
				c.Span = &sp
			} else {
				rec.Error = fmt.Sprintf("chunk %s#%d not found in its document", names[i], k.Index)
			}
			for name, p := range k.Extra {
				if p != nil {
					if c.X == nil {
						c.X = map[string]float64{}
					}
					c.X[name] = *p
				}
			}
			if keepText {
				c.Text = k.Text
			}
			rec.Chunks = append(rec.Chunks, c)
		}
	}
	return rec
}

func loadPrompt(kind, path string) (*prompts.Prompt, error) {
	if path == "" {
		return prompts.Load(kind, "", "")
	}
	return prompts.Load(kind, path, "")
}

func promptName(path string) string {
	if path == "" {
		return "built-in skim_document/DEFAULT"
	}
	return path
}

func inList(list, x string) bool {
	for _, y := range strings.Split(list, ",") {
		if y == x {
			return true
		}
	}
	return false
}

// apiKey reads the key from the environment only, like the CLI.
func apiKey() string {
	for _, name := range []string{"SKIMMER_API_KEY", "JEV_API_KEY", "TYPESAFE_API_KEY"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

func commit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	c := strings.TrimSpace(string(out))
	if st, _ := exec.Command("git", "status", "--porcelain").Output(); len(st) > 0 {
		c += "-dirty"
	}
	return c
}

// fingerprint hashes everything that defines the inputs: goal files, the
// split, the catalog, the corpus, and the prompts.
func fingerprint(corpus string, docs []string, sets, prompt string, extras []skim.Question) string {
	h := sha256.New()
	add := func(path string) {
		b, _ := os.ReadFile(path)
		fmt.Fprintf(h, "%s:%d\n", path, len(b))
		h.Write(b)
	}
	for _, s := range strings.Split(sets, ",") {
		add(kit.GoalSets[s])
	}
	add("evals/gold/split.json")
	add("evals/gold/catalog.json")
	for _, d := range docs {
		add(filepath.Join(corpus, d+".txt"))
	}
	if prompt != "" {
		add(prompt)
	} else {
		add("prompts/skim_document/DEFAULT.md")
	}
	for _, e := range extras {
		fmt.Fprintf(h, "extra:%s:%s\n", e.Name, e.Prompt.Body)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
