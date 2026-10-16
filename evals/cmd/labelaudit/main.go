// labelaudit has a second model family re-check every eval label. For each
// goal it shows the model, through `codex exec`, the goal, its whole
// document split into the catalog's chunks, and the label, and asks for a
// structured verdict. Distractors are checked against the whole corpus.
// The model never sees skimmer's scores. Results go to
// evals/gold/audit_<name>.json; nothing is changed in the goal files.
//
//	go run ./evals/cmd/labelaudit -model gpt-6-astra -out evals/gold/audit_astra.json
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dorkitude/skimmer/evals/internal/kit"
)

const schema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["verdict", "gold_checks", "other_answering_chunks", "answerable", "explanation"],
  "properties": {
    "verdict": {"type": "string", "enum": ["agree", "disagree", "unsure"]},
    "gold_checks": {"type": "array", "items": {
      "type": "object", "additionalProperties": false,
      "required": ["chunk", "supports", "note"],
      "properties": {"chunk": {"type": "string"}, "supports": {"type": "boolean"}, "note": {"type": "string"}}}},
    "other_answering_chunks": {"type": "array", "items": {"type": "string"}},
    "answerable": {"type": "boolean"},
    "explanation": {"type": "string"}
  }
}`

// Verdict is the model's structured answer.
type Verdict struct {
	Verdict    string `json:"verdict"`
	GoldChecks []struct {
		Chunk    string `json:"chunk"`
		Supports bool   `json:"supports"`
		Note     string `json:"note"`
	} `json:"gold_checks"`
	Other       []string `json:"other_answering_chunks"`
	Answerable  bool     `json:"answerable"`
	Explanation string   `json:"explanation"`
}

// Entry is one audited goal.
type Entry struct {
	ID    string   `json:"id"`
	Set   string   `json:"set"`
	Kind  string   `json:"kind"`
	Gold  []string `json:"gold,omitempty"`
	Other string   `json:"other,omitempty"`
	Verdict
	Error string `json:"error,omitempty"`
}

type catChunk struct {
	ID, Doc, Text string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "labelaudit:", err)
		os.Exit(1)
	}
}

func run() error {
	model := flag.String("model", "gpt-6-astra", "codex model")
	effort := flag.String("effort", "high", "reasoning effort")
	out := flag.String("out", "evals/gold/audit_astra.json", "output")
	par := flag.Int("parallel", 6, "concurrent codex calls")
	only := flag.String("only", "", "only these goal ids, comma separated")
	flag.Parse()

	goals, err := kit.LoadGoals([]string{"v1", "v2", "nearmiss"})
	if err != nil {
		return err
	}
	b, err := os.ReadFile("evals/gold/catalog.json")
	if err != nil {
		return err
	}
	var cat []catChunk
	if err := json.Unmarshal(b, &cat); err != nil {
		return err
	}
	byDoc := map[string][]catChunk{}
	var docs []string
	for _, c := range cat {
		if byDoc[c.Doc] == nil {
			docs = append(docs, c.Doc)
		}
		byDoc[c.Doc] = append(byDoc[c.Doc], c)
	}
	tmp, err := os.MkdirTemp("", "labelaudit")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	schemaPath := filepath.Join(tmp, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		return err
	}

	var (
		mu      sync.Mutex
		entries []Entry
		wg      sync.WaitGroup
		sem     = make(chan struct{}, *par)
	)
	for _, g := range goals {
		if *only != "" && !strings.Contains(","+*only+",", ","+g.ID+",") {
			continue
		}
		wg.Add(1)
		go func(g kit.Goal) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			e := Entry{ID: g.ID, Set: g.Set, Kind: g.Kind, Gold: g.Gold(), Other: g.Other}
			v, err := ask(*model, *effort, schemaPath, tmp, prompt(g, byDoc, docs))
			if err != nil {
				e.Error = err.Error()
			} else {
				e.Verdict = v
			}
			mu.Lock()
			entries = append(entries, e)
			fmt.Fprintf(os.Stderr, "labelaudit: %s %s (%d done)\n", g.ID, e.Verdict.Verdict+e.Error, len(entries))
			mu.Unlock()
		}(g)
	}
	wg.Wait()
	sort.Slice(entries, func(a, b int) bool { return entries[a].ID < entries[b].ID })
	res, _ := json.MarshalIndent(map[string]any{
		"auditor": *model + " via codex exec (reasoning effort " + *effort + ")",
		"method":  "Per goal: the goal, its whole document in catalog chunks (all documents for distractors), and the label; a structured verdict. The auditor never saw skimmer's scores.",
		"entries": entries,
	}, "", " ")
	return os.WriteFile(*out, append(res, '\n'), 0o644)
}

func render(chunks []catChunk) string {
	var sb strings.Builder
	for _, c := range chunks {
		fmt.Fprintf(&sb, "\n===== chunk %s =====\n%s\n", c.ID, c.Text)
	}
	return sb.String()
}

func prompt(g kit.Goal, byDoc map[string][]catChunk, docs []string) string {
	var sb strings.Builder
	sb.WriteString(`You are auditing labels in an evaluation set for a tool that, given a reading goal and a document split into chunks, keeps only the chunks that help with the goal. Another model wrote these labels; check them independently and strictly, using only the document text below. Do not run commands or open files; everything you need is in this message.

Consecutive chunks overlap by about 10%, so text near a boundary appears in two chunks. A chunk "supports" the goal when it itself contains the information the goal asks for (or, for a multi-part goal, one of the parts). Ignore tables of contents and headings that merely name a section.

`)
	fmt.Fprintf(&sb, "GOAL (%s, kind %s): %s\n\n", g.ID, g.Kind, g.Goal)
	switch {
	case g.Kind == "distractor":
		sb.WriteString(`LABEL: no document below answers this goal at all.
Task: decide whether any chunk of any document answers the goal, even partly. Set "answerable" true if any does, list those chunk ids in "other_answering_chunks", leave "gold_checks" empty, and set "verdict" to "agree" if the label holds.
`)
		var all []catChunk
		for _, d := range docs {
			all = append(all, byDoc[d]...)
		}
		sb.WriteString(render(all))
		return sb.String()
	case g.Kind == "nearmiss":
		sb.WriteString(`LABEL: this document does NOT contain the answer to the goal (it is on the same subject, but the asked-for fact is absent).
Task: decide whether the document actually answers the goal, fully or partly. Set "answerable" true if it does, list the chunk ids that do in "other_answering_chunks", leave "gold_checks" empty, and set "verdict" to "agree" if the label holds (the document does not answer it).
`)
	case g.Set == "v2":
		fmt.Fprintf(&sb, `LABEL: the goal needs every one of these gold chunks, each holding part of the answer: %s.
Task: for each gold chunk, say in "gold_checks" whether it supports the goal. In "other_answering_chunks", list any OTHER chunk that holds a needed part of the answer not already present in a gold chunk (a missed region); do not list chunks that merely repeat a gold chunk's content through overlap. Set "answerable" to whether the document answers the goal. "verdict" is "agree" only if every gold chunk supports the goal and no needed region is missing.
`, strings.Join(g.Gold(), ", "))
	default:
		fmt.Fprintf(&sb, `LABEL: the answer is in %s (any one of them is enough). Decisive fact, as the labeler wrote it: %q.
`, strings.Join(g.Gold(), ", "), g.DecisiveFact)
		if g.Other != "" {
			fmt.Fprintf(&sb, `This goal is a "confusable": chunk %s is a near-duplicate that must NOT answer the goal.
`, g.Other)
		}
		sb.WriteString(`Task: for each gold chunk, say in "gold_checks" whether it contains the answer. In "other_answering_chunks", list any OTHER chunk that also contains the answer by itself (not merely through the 10% overlap with a gold chunk)`)
		if g.Other != "" {
			fmt.Fprintf(&sb, `; list %s there if it answers the goal`, g.Other)
		}
		sb.WriteString(`. Set "answerable" to whether the document answers the goal. "verdict" is "agree" if a gold chunk contains the answer`)
		if g.Other != "" {
			sb.WriteString(` and the near-duplicate does not`)
		}
		sb.WriteString(".\n")
	}
	sb.WriteString(render(byDoc[g.Doc]))
	return sb.String()
}

func ask(model, effort, schemaPath, tmp, prompt string) (Verdict, error) {
	f, err := os.CreateTemp(tmp, "out-*.json")
	if err != nil {
		return Verdict{}, err
	}
	f.Close()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		cmd := exec.Command("codex", "exec", "-m", model, "-c", "model_reasoning_effort="+effort,
			"--skip-git-repo-check", "-s", "read-only", "--output-schema", schemaPath, "-o", f.Name(), "-")
		cmd.Dir = tmp
		cmd.Stdin = strings.NewReader(prompt)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("codex: %v: %s", err, tail(stderr.String()))
			continue
		}
		b, _ := os.ReadFile(f.Name())
		var v Verdict
		// The file can hold the message twice; the first object is enough.
		if err := json.NewDecoder(bytes.NewReader(b)).Decode(&v); err != nil {
			lastErr = fmt.Errorf("parse %q: %v", tail(string(b)), err)
			continue
		}
		return v, nil
	}
	return Verdict{}, lastErr
}

func tail(s string) string {
	if len(s) > 300 {
		return s[len(s)-300:]
	}
	return s
}
