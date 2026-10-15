// chunkcorpus deterministically chunks the eval corpus with skimmer's own
// chunker and prints chunk catalog JSON to stdout. Offline; no model calls.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dorkitude/skimmer/internal/chunk"
)

type CatalogChunk struct {
	ID        string `json:"id"`
	Doc       string `json:"doc"`
	Text      string `json:"text"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func main() {
	base := "evals/corpus/raw"
	var out []CatalogChunk
	names, _ := os.ReadDir(base)
	var files []string
	for _, e := range names {
		if filepath.Ext(e.Name()) == ".txt" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, name := range files {
		doc := name[:len(name)-len(".txt")]
		b, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		chunks := chunk.Split(string(b), 2000, 200)
		for i, c := range chunks {
			out = append(out, CatalogChunk{
				ID: fmt.Sprintf("%s-%02d", doc, i), Doc: doc, Text: c.Text,
				StartLine: c.StartLine, EndLine: c.EndLine,
			})
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
