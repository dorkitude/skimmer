package main

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

// wilson is the 95% Wilson score interval for k of n.
func wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 1
	}
	const z = 1.959964
	p := float64(k) / float64(n)
	nn := float64(n)
	den := 1 + z*z/nn
	mid := (p + z*z/(2*nn)) / den
	half := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn)) / den
	return math.Max(0, mid-half), math.Min(1, mid+half)
}

// kn renders "k of n (lo–hi)".
func kn(k, n int) string {
	if n == 0 {
		return "–"
	}
	lo, hi := wilson(k, n)
	return fmt.Sprintf("%d/%d (%.2f–%.2f)", k, n, lo, hi)
}

// mcnemar is the exact two-sided McNemar p for b and c discordant pairs.
func mcnemar(b, c int) float64 {
	n := b + c
	if n == 0 {
		return 1
	}
	k := min(b, c)
	p := 0.0
	for i := 0; i <= k; i++ {
		p += math.Exp(lchoose(n, i) - float64(n)*math.Ln2)
	}
	return math.Min(1, 2*p)
}

func lchoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

// BM25 ranks a document's chunks for a query (Robertson/Sparck Jones, k1 1.2,
// b 0.75), with statistics over the chunks being ranked.
type bm25 struct {
	docs  [][]string
	df    map[string]int
	avgdl float64
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be by did do does for from how in is it its of on or
		that the this to was were what when where which who why with each every per their there these those
		than then find list report reported used use using paper page document`) {
		stop[w] = true
	}
}

func tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

func newBM25(texts []string) *bm25 {
	m := &bm25{df: map[string]int{}}
	total := 0
	for _, t := range texts {
		toks := tokens(t)
		m.docs = append(m.docs, toks)
		total += len(toks)
		seen := map[string]bool{}
		for _, w := range toks {
			if !seen[w] {
				seen[w] = true
				m.df[w]++
			}
		}
	}
	if len(texts) > 0 {
		m.avgdl = float64(total) / float64(len(texts))
	}
	return m
}

func (m *bm25) score(query string) []float64 {
	const k1, b = 1.2, 0.75
	n := float64(len(m.docs))
	q := tokens(query)
	out := make([]float64, len(m.docs))
	for i, d := range m.docs {
		tf := map[string]int{}
		for _, w := range d {
			tf[w]++
		}
		for _, w := range q {
			f := float64(tf[w])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(m.df[w])+0.5)/(float64(m.df[w])+0.5))
			out[i] += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(len(d))/m.avgdl))
		}
	}
	return out
}
