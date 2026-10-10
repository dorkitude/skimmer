package chunk

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitPacksParagraphsAndTracksLines(t *testing.T) {
	text := "alpha\nbeta\n\ngamma\n\n\ndelta"
	got := Split(text, 2000, 0)
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d: %+v", len(got), got)
	}
	if got[0].Text != "alpha\nbeta\n\ngamma\n\ndelta" {
		t.Errorf("text = %q", got[0].Text)
	}
	if got[0].StartLine != 1 || got[0].EndLine != 7 {
		t.Errorf("lines = %s", got[0].Lines())
	}
}

func TestSplitRespectsMaxAndLineNumbers(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString(strings.Repeat("x", 90) + "\n\n")
	}
	got := Split(b.String(), 200, 0)
	if len(got) != 25 {
		t.Fatalf("want 25 chunks of two paragraphs, got %d", len(got))
	}
	for _, c := range got {
		if n := utf8.RuneCountInString(c.Text); n > 200 {
			t.Errorf("chunk of %d runes exceeds max", n)
		}
	}
	if got[1].StartLine != 5 || got[1].EndLine != 7 {
		t.Errorf("second chunk lines = %s, want 5-7", got[1].Lines())
	}
}

func TestSplitLongParagraphAndLongLine(t *testing.T) {
	long := strings.Repeat("word ", 100) // one 500-char line
	text := "short line\n" + long + "\nanother"
	got := Split(text, 120, 0)
	for _, c := range got {
		if n := utf8.RuneCountInString(c.Text); n > 120 {
			t.Errorf("chunk of %d runes exceeds max: %q", n, c.Text)
		}
	}
	if got[0].Text != "short line" || got[0].Lines() != "1" {
		t.Errorf("first chunk = %+v", got[0])
	}
	if last := got[len(got)-1]; last.Text != "another" || last.StartLine != 3 {
		t.Errorf("last chunk = %+v", last)
	}
	// A single giant word is hard-cut.
	if got := Split(strings.Repeat("z", 250), 100, 0); len(got) != 3 {
		t.Errorf("hard cut: want 3 chunks, got %d", len(got))
	}
}

func TestSplitEmpty(t *testing.T) {
	if got := Split(" \n\n \n", 100, 0); len(got) != 0 {
		t.Errorf("want no chunks, got %+v", got)
	}
}

func TestSplitOverlap(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "line %02d %s\n", i, strings.Repeat("y", 40)) // 48 runes each
	}
	got := Split(b.String(), 500, 100)
	if len(got) < 2 {
		t.Fatalf("want several chunks, got %d", len(got))
	}
	for i, c := range got {
		if n := utf8.RuneCountInString(c.Text); n > 500 {
			t.Errorf("chunk %d has %d runes, over max with overlap", i, n)
		}
		if i == 0 {
			continue
		}
		prev := got[i-1]
		// The chunk starts inside the previous one, by whole lines...
		if c.StartLine > prev.EndLine || c.StartLine <= prev.StartLine {
			t.Errorf("chunk %d starts at line %d; previous spans %s", i, c.StartLine, prev.Lines())
		}
		// ...and those repeated lines are exactly the previous chunk's tail.
		first := strings.SplitN(c.Text, "\n", 2)[0]
		if !strings.Contains(prev.Text, first) {
			t.Errorf("chunk %d begins with %q, not found in previous chunk", i, first)
		}
		shared := prev.EndLine - c.StartLine + 1
		if shared < 1 || shared*49 > 100+2 {
			t.Errorf("chunk %d shares %d lines; want ~100 runes worth", i, shared)
		}
	}
	// Overlap 0 keeps chunks disjoint.
	plain := Split(b.String(), 500, 0)
	for i := 1; i < len(plain); i++ {
		if plain[i].StartLine != plain[i-1].EndLine+1 {
			t.Errorf("overlap 0: chunk %d starts at %d after %s", i, plain[i].StartLine, plain[i-1].Lines())
		}
	}
}

func TestSplitOverlapTailOfLongLine(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("word ", 200)) // one 999-rune line
	got := Split(long, 300, 50)
	for i, c := range got {
		if n := utf8.RuneCountInString(c.Text); n > 300 {
			t.Errorf("chunk %d has %d runes", i, n)
		}
		if i > 0 && strings.HasPrefix(c.Text, " ") {
			t.Errorf("chunk %d overlap should start at a word: %q", i, c.Text[:20])
		}
	}
	if len(got) < 4 {
		t.Errorf("want ≥4 chunks, got %d", len(got))
	}
}
