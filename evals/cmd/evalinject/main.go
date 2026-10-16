// evalinject writes copies of the eval corpus with one planted line per
// document, one corpus per line in evals/injection/snippets.json, under
// evals/injection/<id>/. The line goes in as its own paragraph at the
// paragraph (or, failing that, sentence) boundary nearest the middle of the
// document. Offline and deterministic; the copies are not committed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dorkitude/skimmer/evals/internal/kit"
)

type snippet struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "evalinject:", err)
		os.Exit(1)
	}
}

func run() error {
	b, err := os.ReadFile("evals/injection/snippets.json")
	if err != nil {
		return err
	}
	var snippets []snippet
	if err := json.Unmarshal(b, &snippets); err != nil {
		return err
	}
	split, err := kit.LoadSplit()
	if err != nil {
		return err
	}
	for _, s := range snippets {
		dir := filepath.Join("evals/injection", s.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for _, d := range split.Docs("all") {
			text, err := kit.LoadDoc(kit.CorpusDir, d)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, d+".txt"), []byte(plant(text, s.Text)), 0o644); err != nil {
				return err
			}
		}
		fmt.Println(dir)
	}
	return nil
}

// plant inserts line as its own paragraph near the middle of text.
func plant(text, line string) string {
	mid := len(text) / 2
	at := nearest(text, "\n\n", mid)
	if at < 0 {
		if at = nearest(text, ". ", mid); at >= 0 {
			at++ // after the period
		}
	}
	if at < 0 {
		at = mid
	}
	return strings.TrimRight(text[:at], " \n") + "\n\n" + line + "\n\n" + strings.TrimLeft(text[at:], " \n")
}

// nearest returns the index of the occurrence of sep closest to pos, or -1.
func nearest(text, sep string, pos int) int {
	best := -1
	for i := 0; ; {
		j := strings.Index(text[i:], sep)
		if j < 0 {
			return best
		}
		j += i
		if best < 0 || abs(j-pos) < abs(best-pos) {
			best = j
		}
		i = j + len(sep)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
