// Package chunk splits text into chunks of bounded size that remember which
// lines they came from, so a kept chunk can be cited as path:start-end.
package chunk

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// DefaultMaxChars is the target chunk size.
const DefaultMaxChars = 2000

// Chunk is a run of text and the 1-based, inclusive line range it spans.
type Chunk struct {
	Text      string `json:"text"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Lines renders the line range as "40-58", or "40" for a single line.
func (c Chunk) Lines() string {
	if c.StartLine == c.EndLine {
		return fmt.Sprint(c.StartLine)
	}
	return fmt.Sprintf("%d-%d", c.StartLine, c.EndLine)
}

// line is one source line and its 1-based number.
type line struct {
	n    int
	text string
}

// Split divides text into chunks of at most maxChars runes. Paragraphs
// (blank-line separated blocks) are packed together up to the limit; a
// paragraph too long on its own is packed line by line, and a line too long
// on its own is cut on word boundaries (then hard-cut). Blank lines between
// paragraphs inside a chunk are kept as a single blank line. maxChars ≤ 0
// uses DefaultMaxChars.
func Split(text string, maxChars int) []Chunk {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// Group lines into paragraphs.
	var paras [][]line
	var cur []line
	for i, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, " \t\r")
		if strings.TrimSpace(l) == "" {
			if len(cur) > 0 {
				paras = append(paras, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, line{i + 1, l})
	}
	if len(cur) > 0 {
		paras = append(paras, cur)
	}

	p := packer{max: maxChars}
	for _, para := range paras {
		if size(para) > maxChars {
			p.flush()
			for _, l := range para {
				if runes(l.text) > maxChars {
					p.flush()
					for _, piece := range cutLine(l.text, maxChars) {
						p.out = append(p.out, Chunk{Text: piece, StartLine: l.n, EndLine: l.n})
					}
					continue
				}
				p.add([]line{l}, "\n")
			}
			p.flush()
			continue
		}
		p.add(para, "\n\n")
	}
	p.flush()
	return p.out
}

type packer struct {
	max   int
	out   []Chunk
	buf   strings.Builder
	n     int // runes in buf
	start int
	end   int
}

// add appends a block of lines, joined to what came before by sep, flushing
// first if it would not fit.
func (p *packer) add(block []line, sep string) {
	text := join(block)
	n := runes(text)
	if p.n > 0 && p.n+runes(sep)+n > p.max {
		p.flush()
	}
	if p.n > 0 {
		p.buf.WriteString(sep)
		p.n += runes(sep)
	} else {
		p.start = block[0].n
	}
	p.buf.WriteString(text)
	p.n += n
	p.end = block[len(block)-1].n
}

func (p *packer) flush() {
	if p.n > 0 {
		p.out = append(p.out, Chunk{Text: p.buf.String(), StartLine: p.start, EndLine: p.end})
	}
	p.buf.Reset()
	p.n = 0
}

func join(ls []line) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.text
	}
	return strings.Join(parts, "\n")
}

func size(ls []line) int {
	n := 0
	for i, l := range ls {
		if i > 0 {
			n++
		}
		n += runes(l.text)
	}
	return n
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// cutLine splits one oversized line on word boundaries, hard-cutting any
// single word longer than max.
func cutLine(s string, max int) []string {
	var out []string
	var cur strings.Builder
	n := 0
	for _, w := range strings.Fields(s) {
		for runes(w) > max {
			if n > 0 {
				out = append(out, cur.String())
				cur.Reset()
				n = 0
			}
			r := []rune(w)
			out = append(out, string(r[:max]))
			w = string(r[max:])
		}
		wn := runes(w)
		if n > 0 && n+1+wn > max {
			out = append(out, cur.String())
			cur.Reset()
			n = 0
		}
		if n > 0 {
			cur.WriteByte(' ')
			n++
		}
		cur.WriteString(w)
		n += wn
	}
	if n > 0 {
		out = append(out, cur.String())
	}
	return out
}
