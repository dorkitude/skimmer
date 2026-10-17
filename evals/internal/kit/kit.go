// Package kit holds what the eval runner and the eval report share: goal
// sets, the dev/holdout split, the corpus, and scoring by document span.
//
// Scoring by span is what makes results comparable across chunk sizes and
// boundary shifts. Every chunk, of any size, is located in its source
// document as a span of non-whitespace runes, and a goal's gold is the span
// of its gold chunk in the 2000-character catalog. A goal's gold region is
// "kept" when the kept chunks cover it (see Hit), whatever their ids.
package kit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Goal is one frozen eval goal. Gold is a catalog chunk id or a list of
// them; for v2 goals each listed chunk is a separate region ("mode") the
// goal needs.
type Goal struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Goal         string `json:"goal"`
	Doc          string `json:"doc,omitempty"`
	GoldRaw      any    `json:"gold,omitempty"`
	DecisiveFact string `json:"decisive_fact,omitempty"`
	Other        string `json:"other,omitempty"` // confusable: the near-duplicate chunk
	// Set is the goal file it came from: v1, v2 or nearmiss.
	Set string `json:"-"`
}

// Gold returns the goal's gold chunk ids.
func (g Goal) Gold() []string {
	switch v := g.GoldRaw.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Negative reports whether no chunk should help with the goal.
func (g Goal) Negative() bool { return g.Kind == "distractor" || g.Kind == "nearmiss" }

// GoalSets maps set names to their files.
var GoalSets = map[string]string{
	"v1":       "evals/gold/goals.json",
	"v2":       "evals/gold/goals-v2.json",
	"nearmiss": "evals/gold/nearmiss.json",
}

// LoadGoals reads the named sets, in order.
func LoadGoals(sets []string) ([]Goal, error) {
	var out []Goal
	for _, set := range sets {
		path, ok := GoalSets[set]
		if !ok {
			return nil, fmt.Errorf("unknown goal set %q", set)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var gs []Goal
		if err := json.Unmarshal(b, &gs); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for i := range gs {
			gs[i].Set = set
		}
		out = append(out, gs...)
	}
	return out, nil
}

// Split is the frozen dev/holdout split (evals/gold/split.json). Documents
// are split, not goals, so no document is in both halves. Distractor goals
// have no document; they are split by id and judged over their half's
// documents.
type Split struct {
	Dev         []string `json:"dev_docs"`
	Holdout     []string `json:"holdout_docs"`
	Distractors struct {
		Dev     []string `json:"dev"`
		Holdout []string `json:"holdout"`
	} `json:"distractors"`
}

// LoadSplit reads evals/gold/split.json.
func LoadSplit() (*Split, error) {
	b, err := os.ReadFile("evals/gold/split.json")
	if err != nil {
		return nil, err
	}
	var s Split
	return &s, json.Unmarshal(b, &s)
}

// Of returns "dev" or "holdout" for a goal.
func (s *Split) Of(g Goal) string {
	if g.Doc == "" {
		if slices.Contains(s.Distractors.Holdout, g.ID) {
			return "holdout"
		}
		return "dev"
	}
	if slices.Contains(s.Holdout, g.Doc) {
		return "holdout"
	}
	return "dev"
}

// Docs returns the documents of a half ("dev", "holdout", or "all").
func (s *Split) Docs(half string) []string {
	switch half {
	case "dev":
		return s.Dev
	case "holdout":
		return s.Holdout
	}
	all := append(append([]string{}, s.Dev...), s.Holdout...)
	sort.Strings(all)
	return all
}

// CorpusDir holds the raw documents.
const CorpusDir = "evals/corpus/raw"

// LoadDoc returns a document's text.
func LoadDoc(dir, doc string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, doc+".txt"))
	return string(b), err
}

// Shift drops the first n runes of a document, moving every chunk boundary.
func Shift(text string, n int) string {
	r := []rune(text)
	if n <= 0 || n >= len(r) {
		return text
	}
	return string(r[n:])
}

// Squeeze removes all whitespace. Chunks rejoin lines with their own
// separators, so a chunk is a contiguous run of its document only once
// whitespace is ignored.
func Squeeze(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Span is a half-open byte range of a document with its whitespace removed.
type Span struct {
	A int `json:"a"`
	B int `json:"b"`
}

// Len is the span's length.
func (s Span) Len() int { return max(0, s.B-s.A) }

// Locator finds chunks in one document.
type Locator struct {
	doc    string
	pos    int  // where the previous chunk started; chunks come in order
	insert Span // inserted bytes in doc, excluded from original-document coordinates
}

// NewLocator indexes a document's text.
func NewLocator(text string) *Locator { return &Locator{doc: Squeeze(text)} }

// NewMappedLocator locates chunks of modified in original-document coordinates.
// modified must be original with at most one contiguous insertion (as made by
// evalinject). Chunks may contain any part of the insertion; only their original
// content contributes to the returned span. A wholly inserted chunk has length 0.
func NewMappedLocator(original, modified string) (*Locator, error) {
	orig, mod := Squeeze(original), Squeeze(modified)
	if len(mod) < len(orig) {
		return nil, fmt.Errorf("modified document is not the original plus an insertion")
	}
	a := 0
	for a < len(orig) && orig[a] == mod[a] {
		a++
	}
	b := a + len(mod) - len(orig)
	if orig[a:] != mod[b:] {
		return nil, fmt.Errorf("modified document is not the original plus an insertion")
	}
	return &Locator{doc: mod, insert: Span{a, b}}, nil
}

func (l *Locator) originalSpan(a, b int) Span {
	mapOffset := func(p int) int {
		return p - min(max(p-l.insert.A, 0), l.insert.Len())
	}
	return Span{mapOffset(a), mapOffset(b)}
}

// Locate returns the span of a chunk of this document. Chunks are found in
// document order, so each search starts where the previous chunk began.
func (l *Locator) Locate(chunk string) (Span, bool) {
	c := Squeeze(chunk)
	if len(c) == 0 {
		return Span{}, false
	}
	if i := strings.Index(l.doc[l.pos:], c); i >= 0 {
		l.pos += i
		return l.originalSpan(l.pos, l.pos+len(c)), true
	}
	if i := strings.Index(l.doc, c); i >= 0 {
		return l.originalSpan(i, i+len(c)), true
	}
	return Span{}, false
}

// Region is a part of a document a goal needs, as a set of spans.
type Region []Span

// Len is the region's size.
func (r Region) Len() int {
	n := 0
	for _, s := range r {
		n += s.Len()
	}
	return n
}

// Minus removes o's spans from r.
func (r Region) Minus(o Region) Region {
	out := append(Region{}, r...)
	for _, cut := range o {
		var next Region
		for _, s := range out {
			if cut.B <= s.A || cut.A >= s.B {
				next = append(next, s)
				continue
			}
			if s.A < cut.A {
				next = append(next, Span{s.A, cut.A})
			}
			if cut.B < s.B {
				next = append(next, Span{cut.B, s.B})
			}
		}
		out = next
	}
	return out
}

// overlap is how much of r lies inside s.
func (r Region) overlap(s Span) int {
	n := 0
	for _, x := range r {
		n += max(0, min(x.B, s.B)-max(x.A, s.A))
	}
	return n
}

// covered is how much of r lies inside the union of spans.
func (r Region) covered(spans []Span) int {
	n := 0
	for _, x := range r {
		var parts []Span
		for _, s := range spans {
			a, b := max(x.A, s.A), min(x.B, s.B)
			if a < b {
				parts = append(parts, Span{a, b})
			}
		}
		sort.Slice(parts, func(i, j int) bool { return parts[i].A < parts[j].A })
		end := x.A
		for _, p := range parts {
			if p.B <= end {
				continue
			}
			n += p.B - max(p.A, end)
			end = p.B
		}
	}
	return n
}

// HitBy reports whether one chunk keeps the region: it holds at least half
// of the region, or at least half of the chunk lies inside the region (a
// chunk smaller than the region that is mostly about it).
func (r Region) HitBy(s Span) bool {
	o := r.overlap(s)
	return o > 0 && (2*o >= r.Len() || 2*o >= s.Len())
}

// Hit reports whether a set of kept chunks keeps the region: one of them
// does (HitBy), or together they cover at least half of it. A neighbor that
// only shares the chunk overlap (10%) with a region does not keep it.
func (r Region) Hit(kept []Span) bool {
	if r.Len() == 0 {
		return false
	}
	return slices.ContainsFunc(kept, r.HitBy) || 2*r.covered(kept) >= r.Len()
}

// Catalog maps catalog chunk ids to their spans.
type Catalog map[string]Span

// LoadCatalog reads evals/gold/catalog.json and locates every chunk.
func LoadCatalog() (Catalog, error) {
	b, err := os.ReadFile("evals/gold/catalog.json")
	if err != nil {
		return nil, err
	}
	var chunks []struct {
		ID, Doc, Text string
	}
	if err := json.Unmarshal(b, &chunks); err != nil {
		return nil, err
	}
	cat := Catalog{}
	locs := map[string]*Locator{}
	for _, c := range chunks {
		l := locs[c.Doc]
		if l == nil {
			text, err := LoadDoc(CorpusDir, c.Doc)
			if err != nil {
				return nil, err
			}
			l = NewLocator(text)
			locs[c.Doc] = l
		}
		s, ok := l.Locate(c.Text)
		if !ok {
			return nil, fmt.Errorf("catalog chunk %s not found in its document", c.ID)
		}
		cat[c.ID] = s
	}
	return cat, nil
}

// Regions returns the regions a goal needs, one per gold chunk. A
// confusable goal's region excludes what its gold chunk shares with the
// near-duplicate, and Other is the near-duplicate's own region.
func (c Catalog) Regions(g Goal) (gold []Region, other Region) {
	if g.Other != "" {
		other = Region{c[g.Other]}
	}
	for _, id := range g.Gold() {
		r := Region{c[id]}
		if other != nil {
			r = r.Minus(other)
		}
		gold = append(gold, r)
	}
	if other != nil {
		for _, id := range g.Gold() {
			other = other.Minus(Region{c[id]})
		}
	}
	return gold, other
}
