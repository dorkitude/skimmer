// Package skim turns documents (web pages, files, stdin) into the few chunks
// Jev believes help with a goal.
package skim

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dorkitude/skimmer/internal/chunk"
	"github.com/dorkitude/skimmer/internal/fetch"
	"github.com/dorkitude/skimmer/internal/jev"
	"github.com/dorkitude/skimmer/internal/prompts"
)

// Kind is where a source's text comes from.
type Kind string

const (
	KindURL   Kind = "url"
	KindFile  Kind = "file"
	KindStdin Kind = "stdin"
)

// Source is one document to skim.
type Source struct {
	Kind Kind
	// Ref is the URL or file path; "-" for stdin.
	Ref string
	// Text is the content for KindStdin.
	Text string
}

// Kept is one chunk Jev judged relevant (or could not judge).
type Kept struct {
	Index     int      `json:"index"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	P         *float64 `json:"p"` // nil: Jev never ruled on it, kept to be safe
	Text      string   `json:"text"`
}

// Lines renders the kept chunk's line range.
func (k Kept) Lines() string {
	return chunk.Chunk{StartLine: k.StartLine, EndLine: k.EndLine}.Lines()
}

// Result is the outcome of skimming one source.
type Result struct {
	Source    string `json:"source"`
	Kind      Kind   `json:"kind"`
	Error     string `json:"error,omitempty"`
	Chunks    int    `json:"chunks"`
	Chars     int    `json:"chars"`
	KeptChars int    `json:"kept_chars"`
	Truncated bool   `json:"truncated,omitempty"`
	Unjudged  int    `json:"unjudged,omitempty"`
	Kept      []Kept `json:"kept"`
}

// Skimmer holds the shared settings for a run.
type Skimmer struct {
	Client  *jev.Client
	Fetcher *fetch.Fetcher
	Prompt  *prompts.Prompt

	Goal       string
	Threshold  float64 // keep chunks with P(yes) ≥ Threshold
	ChunkChars int
	// Overlap is the fraction of each chunk repeated from the one before.
	Overlap  float64
	MaxChars int // per source, in runes
	// MaxKept, when > 0, keeps at most this many chunks per source (the
	// highest scoring), still emitted in document order.
	MaxKept int
	// Parallel bounds how many sources are loaded and chunked at once.
	Parallel int
}

// Run skims every source and returns results aligned with sources, plus the
// total Jev usage. Per-source failures are recorded in Result.Error.
func (s *Skimmer) Run(ctx context.Context, sources []Source) ([]Result, jev.Usage) {
	results := make([]Result, len(sources))
	par := s.Parallel
	if par <= 0 {
		par = 8
	}
	var (
		mu    sync.Mutex
		usage jev.Usage
		wg    sync.WaitGroup
		sem   = make(chan struct{}, par)
	)
	for i, src := range sources {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, src Source) {
			defer wg.Done()
			defer func() { <-sem }()
			r, u := s.one(ctx, src)
			results[i] = r
			mu.Lock()
			usage.Add(u)
			mu.Unlock()
		}(i, src)
	}
	wg.Wait()
	return results, usage
}

func (s *Skimmer) one(ctx context.Context, src Source) (Result, jev.Usage) {
	r := Result{Source: src.Ref, Kind: src.Kind, Kept: []Kept{}}
	text, err := s.load(ctx, src)
	if err != nil {
		r.Error = err.Error()
		return r, jev.Usage{}
	}
	if s.MaxChars > 0 && utf8.RuneCountInString(text) > s.MaxChars {
		text = string([]rune(text)[:s.MaxChars])
		r.Truncated = true
	}
	r.Chars = utf8.RuneCountInString(text)
	chunks := chunk.Split(text, s.ChunkChars, int(s.Overlap*float64(s.ChunkChars)))
	r.Chunks = len(chunks)
	if len(chunks) == 0 {
		return r, jev.Usage{}
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	label := src.Ref
	if src.Kind == KindStdin {
		label = ""
	}
	ps, usage, err := s.Client.Judge(ctx, jev.Judgement{
		Goal:    s.Goal,
		Context: label,
		Texts:   texts,
		Instructions: func(i int) (string, error) {
			return s.Prompt.Render(prompts.Data{
				Goal: s.Goal, ID: jev.ItemKey(i), Index: i, Text: texts[i],
				Source: label, Lines: chunks[i].Lines(),
			})
		},
	})
	var partial *jev.PartialError
	if err != nil && !errors.As(err, &partial) {
		r.Error = err.Error()
		return r, usage
	}

	// Pick the chunks to keep: P ≥ threshold, or unjudged.
	var keep []int
	for i, p := range ps {
		if p == nil {
			r.Unjudged++
			keep = append(keep, i)
		} else if *p >= s.Threshold {
			keep = append(keep, i)
		}
	}
	if s.MaxKept > 0 && len(keep) > s.MaxKept {
		sort.SliceStable(keep, func(a, b int) bool { return score(ps[keep[a]]) > score(ps[keep[b]]) })
		keep = keep[:s.MaxKept]
		sort.Ints(keep)
	}
	for _, i := range keep {
		c := chunks[i]
		r.Kept = append(r.Kept, Kept{Index: i, StartLine: c.StartLine, EndLine: c.EndLine, P: ps[i], Text: c.Text})
		r.KeptChars += utf8.RuneCountInString(c.Text)
	}
	return r, usage
}

// score orders chunks for MaxKept; unjudged chunks rank last.
func score(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func (s *Skimmer) load(ctx context.Context, src Source) (string, error) {
	switch src.Kind {
	case KindURL:
		return s.Fetcher.Fetch(ctx, src.Ref)
	case KindFile:
		return ReadFile(src.Ref)
	case KindStdin:
		if strings.TrimSpace(src.Text) == "" {
			return "", errors.New("stdin is empty")
		}
		return src.Text, nil
	}
	return "", fmt.Errorf("unknown source kind %q", src.Kind)
}

// ErrBinary marks a file that is not text.
var ErrBinary = errors.New("binary file (skipped)")

// ReadFile returns a file's text. PDFs yield their text layer; other binary
// files are rejected.
func ReadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if fetch.IsPDF("", b) {
		return fetch.PDFToText(b)
	}
	if IsBinary(b) {
		return "", ErrBinary
	}
	return string(b), nil
}

// IsBinary sniffs the first 8 KiB for NUL bytes, which text never has.
func IsBinary(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// skipDirs are never descended into when a directory is given.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"__pycache__": true, "dist": true, "build": true, ".next": true, "target": true,
	".idea": true, ".vscode": true, ".cache": true,
}

// ExpandDir lists the regular files under dir, skipping hidden and vendored
// directories, up to max files (0 = no limit). The bool reports truncation.
func ExpandDir(dir string, max int) ([]string, bool, error) {
	var files []string
	truncated := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if max > 0 && len(files) >= max {
			truncated = true
			return filepath.SkipAll
		}
		files = append(files, path)
		return nil
	})
	return files, truncated, err
}
