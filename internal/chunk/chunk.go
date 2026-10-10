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

// DefaultOverlap is the default fraction of a chunk repeated from the end
// of the chunk before it.
const DefaultOverlap = 0.10

// Split divides text into chunks of at most maxChars runes. Paragraphs
// (blank-line separated blocks) are packed together up to the limit; a
// paragraph too long on its own is packed line by line, and a line too long
// on its own is cut on word boundaries (then hard-cut). Blank lines between
// paragraphs inside a chunk are kept as a single blank line. maxChars ≤ 0
// uses DefaultMaxChars.
//
// With overlap > 0, each chunk after the first begins with up to overlap
// runes of whole trailing lines from the chunk before it (or the tail of
// its last line, from a word boundary, when that line alone is too long),
// so something that straddles a boundary is seen whole at least once. The
// packing budget shrinks to match, so chunks still fit in maxChars. Overlap
// is capped at half of maxChars.
func Split(text string, maxChars, overlap int) []Chunk {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	overlap = max(0, min(overlap, maxChars/2))
	budget := maxChars
	if overlap > 0 {
		budget = maxChars - overlap - 2 // 2: the separator joining overlap and chunk
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

	p := packer{max: budget}
	for _, para := range paras {
		if size(para) > budget {
			p.flush()
			for _, l := range para {
				if runes(l.text) > budget {
					p.flush()
					for k, piece := range cutLine(l.text, budget) {
						sep := " " // later pieces continue the same line
						if k == 0 {
							sep = "\n"
						}
						p.out = append(p.out, []seg{{n: l.n, text: piece, sep: sep}})
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

	out := make([]Chunk, len(p.out))
	for i, g := range p.out {
		if i > 0 && overlap > 0 {
			g = append(tail(p.out[i-1], overlap), g...)
		}
		out[i] = render(g)
	}
	return out
}

// seg is one line (or piece of a line) inside a chunk, with the separator
// that joins it to the seg before it.
type seg struct {
	n    int
	text string
	sep  string
}

type packer struct {
	max  int
	segs []seg
	size int // runes in the chunk being built
	out  [][]seg
}

// add appends a block of lines, joined to what came before by sep, flushing
// first if it would not fit.
func (p *packer) add(block []line, sep string) {
	n := size(block)
	if p.size > 0 && p.size+runes(sep)+n > p.max {
		p.flush()
	}
	if p.size > 0 {
		p.size += runes(sep)
	}
	for i, l := range block {
		s := "\n"
		if i == 0 {
			s = sep
		}
		p.segs = append(p.segs, seg{n: l.n, text: l.text, sep: s})
	}
	p.size += n
}

func (p *packer) flush() {
	if len(p.segs) > 0 {
		p.out = append(p.out, p.segs)
	}
	p.segs = nil
	p.size = 0
}

// tail returns the trailing segs of g that fit in max runes, separators
// included. If even the last seg is too long, it returns that seg's last
// max runes, starting after a space when there is one.
func tail(g []seg, max int) []seg {
	total, i := 0, len(g)
	for i > 0 {
		cost := runes(g[i-1].text)
		if i < len(g) {
			cost += runes(g[i].sep)
		}
		if total+cost > max {
			break
		}
		total += cost
		i--
	}
	if i < len(g) {
		return g[i:]
	}
	last := g[len(g)-1]
	r := []rune(last.text)
	t := string(r[len(r)-max:])
	if j := strings.IndexByte(t, ' '); j >= 0 && j < len(t)-1 {
		t = t[j+1:]
	}
	return []seg{{n: last.n, text: t, sep: last.sep}}
}

// render joins segs into a chunk; the first seg's separator is dropped.
func render(g []seg) Chunk {
	var b strings.Builder
	for i, s := range g {
		if i > 0 {
			b.WriteString(s.sep)
		}
		b.WriteString(s.text)
	}
	return Chunk{Text: b.String(), StartLine: g[0].n, EndLine: g[len(g)-1].n}
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
