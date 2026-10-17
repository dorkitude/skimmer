package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/dorkitude/skimmer/evals/internal/kit"
)

// Snippet is a line planted in every document by evalinject.
type Snippet struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func loadSnippets(path string) ([]Snippet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s []Snippet
	return s, json.Unmarshal(b, &s)
}

// isPlanted reports whether a chunk holds most of a planted line (its
// middle 40 non-space characters).
func isPlanted(text string, lines []Snippet) bool {
	t := kit.Squeeze(text)
	for _, l := range lines {
		s := kit.Squeeze(l.Text)
		mid := s
		if len(s) > 40 {
			mid = s[len(s)/2-20 : len(s)/2+20]
		}
		if strings.Contains(t, mid) {
			return true
		}
	}
	return false
}
