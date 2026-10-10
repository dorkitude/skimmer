package chunk

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitPacksParagraphsAndTracksLines(t *testing.T) {
	text := "alpha\nbeta\n\ngamma\n\n\ndelta"
	got := Split(text, 2000)
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
	got := Split(b.String(), 200)
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
	got := Split(text, 120)
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
	if got := Split(strings.Repeat("z", 250), 100); len(got) != 3 {
		t.Errorf("hard cut: want 3 chunks, got %d", len(got))
	}
}

func TestSplitEmpty(t *testing.T) {
	if got := Split(" \n\n \n", 100); len(got) != 0 {
		t.Errorf("want no chunks, got %+v", got)
	}
}
