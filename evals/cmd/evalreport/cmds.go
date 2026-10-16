package main

import (
	"flag"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/dorkitude/skimmer/evals/internal/kit"
)

func halves(m map[string]*goalOutcomes) []string {
	seen := map[string]bool{}
	for _, g := range m {
		seen[g.Half] = true
	}
	var out []string
	for _, h := range []string{"dev", "holdout"} {
		if seen[h] {
			out = append(out, h)
		}
	}
	return out
}

func pick(m map[string]*goalOutcomes, half, grp string) []*goalOutcomes {
	var out []*goalOutcomes
	for _, g := range sortedGoals(m) {
		if g.Half != half {
			continue
		}
		switch grp {
		case "all positive":
			if g.Goal.Negative() {
				continue
			}
		case "all negative":
			if !g.Goal.Negative() {
				continue
			}
		default:
			if group(g.Goal) != grp {
				continue
			}
		}
		out = append(out, g)
	}
	return out
}

var rowGroups = append(append([]string{}, groupOrder...), "all positive", "all negative")

func describe(run *Run, rule Rule) {
	models := map[string]bool{}
	unjudged, failed, unlocated := 0, 0, 0
	for _, r := range run.Recs {
		models[r.Model] = true
		unjudged += r.Unjudged
		if r.Error != "" {
			failed++
		}
		for _, c := range r.Chunks {
			if c.Span == nil {
				unlocated++
			}
		}
	}
	var ms []string
	for m := range models {
		ms = append(ms, m)
	}
	sort.Strings(ms)
	fmt.Printf("Run `%s`: model %s (answered: %s), prompt `%s`", run.H.Run, run.H.Model, strings.Join(ms, ", "), run.H.Prompt)
	if len(run.H.Extra) > 0 {
		fmt.Printf(", extra %v", run.H.Extra)
	}
	fmt.Printf(", %d-char chunks, overlap %.2f", run.H.ChunkChars, run.H.Overlap)
	if run.H.Shift > 0 {
		fmt.Printf(", shifted %d runes", run.H.Shift)
	}
	fmt.Printf(", %d draws, %d goal-draws, commit %s, fingerprint `%s`. Keep rule: %s. Unjudged chunks: %d. Failed goal-draws: %d. Chunks not located: %d.\n\n",
		run.H.Draws, len(run.Recs), run.H.Commit, run.H.Finger, rule, unjudged, failed, unlocated)
}

func cmdSummary(args []string) error {
	f := flag.NewFlagSet("summary", flag.ContinueOnError)
	path := f.String("run", "", "archive")
	score := f.String("score", "p", "p, weighted:W, or gate:G")
	t := f.Float64("t", 0.5, "keep threshold")
	check := f.Bool("check", false, "drop chunks the injection check flags")
	half := f.String("half", "all", "dev, holdout, or all")
	noBaseline := f.Bool("no-baseline", false, "skip the BM25 table")
	if err := f.Parse(args); err != nil {
		return err
	}
	env, err := loadEnv()
	if err != nil {
		return err
	}
	run, err := loadRun(*path)
	if err != nil {
		return err
	}
	rule := parseRule(*score, *t, *check)
	m := env.collect(run, rule, *half)
	describe(run, rule)

	fmt.Println("| half | goals | n | complete, or leak for negatives (most draws) | per draw | near-duplicate kept | kept chunks/goal | kept chars/goal | input tokens/goal |")
	fmt.Println("|---|---|---:|---|---|---|---:|---:|---:|")
	for _, h := range halves(m) {
		for _, grp := range rowGroups {
			gs := pick(m, h, grp)
			if len(gs) == 0 {
				continue
			}
			neg := gs[0].Goal.Negative() && grp != "all positive"
			f := isComplete
			if neg {
				f = isLeak
			}
			k, other := 0, 0
			perDraw := map[int]int{}
			var kept, chars, toks []float64
			for _, g := range gs {
				if majority(g.Outs, f) {
					k++
				}
				if majority(g.Outs, isOther) {
					other++
				}
				for i, o := range g.Outs {
					if f(o) {
						perDraw[g.Recs[i].Draw]++
					}
					kept = append(kept, float64(o.Kept))
					chars = append(chars, float64(o.KeptChars))
					toks = append(toks, float64(g.Recs[i].InputTokens))
				}
			}
			var pd []string
			for d := 1; d <= run.H.Draws; d++ {
				pd = append(pd, fmt.Sprint(perDraw[d]))
			}
			oth := ""
			if grp == "confusable" {
				oth = kn(other, len(gs))
			}
			fmt.Printf("| %s | %s | %d | %s | %s | %s | %.2f | %s | %s |\n", h, grp, len(gs), kn(k, len(gs)), strings.Join(pd, ", "), oth, mean(kept), commas(mean(chars)), commas(mean(toks)))
		}
	}
	fmt.Println()

	fmt.Println("Ranked (mean over goal-draws; a gold region's rank is that of the first chunk that holds it):")
	fmt.Println()
	fmt.Println("| half | goals | goal-draws | R@1 | R@3 | R@5 | R@10 | all-modes@10 |")
	fmt.Println("|---|---|---:|---:|---:|---:|---:|---:|")
	for _, h := range halves(m) {
		for _, grp := range append(append([]string{}, groupOrder[:5]...), "all positive") {
			gs := pick(m, h, grp)
			if len(gs) == 0 {
				continue
			}
			var outs []Outcome
			for _, g := range gs {
				outs = append(outs, g.Outs...)
			}
			r := ranked(outs)
			fmt.Printf("| %s | %s | %d | %.3f | %.3f | %.3f | %.3f | %.3f |\n", h, grp, len(outs), r[0], r[1], r[2], r[3], r[4])
		}
	}
	fmt.Println()

	if !*noBaseline {
		if err := baseline(env, run, rule, m); err != nil {
			return err
		}
	}
	stability(run, m, rule)
	return nil
}

// ranked returns R@1, R@3, R@5, R@10 and all-modes@10.
func ranked(outs []Outcome) [5]float64 {
	var r [5]float64
	for _, o := range outs {
		for i, k := range []int{1, 3, 5, 10} {
			for _, rk := range o.Ranks {
				if rk > 0 && rk <= k {
					r[i]++
					break
				}
			}
		}
		all := len(o.Ranks) > 0
		for _, rk := range o.Ranks {
			if rk == 0 || rk > 10 {
				all = false
			}
		}
		if all {
			r[4]++
		}
	}
	for i := range r {
		r[i] /= float64(len(outs))
	}
	return r
}

// baseline ranks the same chunks with BM25 and keeps, for each goal and
// draw, as many as skimmer kept.
func baseline(env *Env, run *Run, rule Rule, m map[string]*goalOutcomes) error {
	type pair struct{ jev, bm Outcome }
	res := map[string][]pair{}
	cache := map[string][]string{}
	for _, g := range m {
		if g.Goal.Negative() {
			continue
		}
		texts, ok := cache[g.Goal.Doc]
		if !ok {
			var err error
			if texts, err = chunksOf(run, g.Goal.Doc); err != nil {
				return err
			}
			cache[g.Goal.Doc] = texts
		}
		scores := newBM25(texts).score(g.Goal.Goal)
		for i, rec := range g.Recs {
			bmRec := rec
			bmRec.Chunks = nil
			order := make([]int, len(texts))
			for k := range order {
				order[k] = k
			}
			sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })
			spans := map[int]*kit.Span{}
			for _, c := range rec.Chunks {
				spans[c.I] = c.Span
			}
			// Scores that fall in BM25 order and are ≥ 1 for exactly the top k,
			// so a threshold of 1 keeps as many chunks as skimmer did.
			k := g.Outs[i].Kept
			for rank, idx := range order {
				p := 1 - float64(rank+1)/float64(len(order)+1)
				if rank < k {
					p++
				}
				bmRec.Chunks = append(bmRec.Chunks, Chunk{Doc: g.Goal.Doc, I: idx, Span: spans[idx], P: &p})
			}
			bmRule := Rule{Mode: "p", T: 1}
			res[g.Goal.ID] = append(res[g.Goal.ID], pair{g.Outs[i], env.outcome(bmRec, bmRule)})
		}
	}
	fmt.Println("Keyword baseline: BM25 over the same chunks, keeping as many per goal and draw as skimmer kept (complete in most draws; exact McNemar on the paired goals):")
	fmt.Println()
	fmt.Println("| half | goals | n | skimmer complete | BM25 complete | skimmer only | BM25 only | p | skimmer R@3 | BM25 R@3 | skimmer R@10 | BM25 R@10 |")
	fmt.Println("|---|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|")
	for _, h := range halves(m) {
		for _, grp := range append(append([]string{}, groupOrder[:5]...), "all positive") {
			gs := pick(m, h, grp)
			if len(gs) == 0 {
				continue
			}
			kj, kb, b, c := 0, 0, 0, 0
			var jo, bo []Outcome
			for _, g := range gs {
				var js, bs []Outcome
				for _, p := range res[g.Goal.ID] {
					js = append(js, p.jev)
					bs = append(bs, p.bm)
				}
				jc, bc := majority(js, isComplete), majority(bs, isComplete)
				if jc {
					kj++
				}
				if bc {
					kb++
				}
				if jc && !bc {
					b++
				}
				if bc && !jc {
					c++
				}
				jo = append(jo, js...)
				bo = append(bo, bs...)
			}
			rj, rb := ranked(jo), ranked(bo)
			fmt.Printf("| %s | %s | %d | %s | %s | %d | %d | %.3g | %.3f | %.3f | %.3f | %.3f |\n", h, grp, len(gs), kn(kj, len(gs)), kn(kb, len(gs)), b, c, mcnemar(b, c), rj[1], rb[1], rj[3], rb[3])
		}
	}
	fmt.Println()
	return nil
}

// stability compares draws pairwise: chunks scored in both draws that land
// on opposite sides of the threshold, and how far scores move.
func stability(run *Run, m map[string]*goalOutcomes, rule Rule) {
	if run.H.Draws < 2 {
		return
	}
	type key struct{ a, b int }
	type acc struct {
		n, flips, goalsFlipped int
		sum, max               float64
	}
	stats := map[key]*acc{}
	for _, g := range m {
		byDraw := map[int]map[string]float64{}
		for _, rec := range g.Recs {
			s := map[string]float64{}
			for _, c := range rec.Chunks {
				if c.P != nil {
					s[fmt.Sprintf("%s#%d", c.Doc, c.I)] = rule.score(c)
				}
			}
			byDraw[rec.Draw] = s
		}
		for a := 1; a <= run.H.Draws; a++ {
			for b := a + 1; b <= run.H.Draws; b++ {
				sa, sb := byDraw[a], byDraw[b]
				if sa == nil || sb == nil {
					continue
				}
				k := key{a, b}
				st := stats[k]
				if st == nil {
					st = &acc{}
					stats[k] = st
				}
				flipped := false
				for id, pa := range sa {
					pb, ok := sb[id]
					if !ok {
						continue
					}
					st.n++
					d := math.Abs(pa - pb)
					st.sum += d
					st.max = math.Max(st.max, d)
					if (pa >= rule.T) != (pb >= rule.T) {
						st.flips++
						flipped = true
					}
				}
				if flipped {
					st.goalsFlipped++
				}
			}
		}
	}
	var keys []key
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	fmt.Printf("Draw stability (every goal; a flip is a chunk on opposite sides of %.2f in the two draws):\n\n", rule.T)
	fmt.Println("| draws | chunks scored in both | flips | goals with a flip | mean abs. change | max abs. change |")
	fmt.Println("|---|---:|---:|---:|---:|---:|")
	tot := acc{}
	for _, k := range keys {
		st := stats[k]
		fmt.Printf("| %d vs %d | %s | %d | %d of %d | %.4f | %.2f |\n", k.a, k.b, commas(float64(st.n)), st.flips, st.goalsFlipped, len(m), st.sum/float64(st.n), st.max)
		tot.n += st.n
		tot.flips += st.flips
		tot.sum += st.sum
		tot.max = math.Max(tot.max, st.max)
	}
	if len(keys) > 1 {
		fmt.Printf("| all pairs | %s | %d | | %.4f | %.2f |\n", commas(float64(tot.n)), tot.flips, tot.sum/float64(tot.n), tot.max)
	}
	fmt.Println()
}

func cmdSweep(args []string) error {
	f := flag.NewFlagSet("sweep", flag.ContinueOnError)
	path := f.String("run", "", "archive")
	score := f.String("score", "p", "p, weighted:W, or gate:G")
	half := f.String("half", "dev", "dev, holdout, or all")
	check := f.Bool("check", false, "drop chunks the injection check flags")
	if err := f.Parse(args); err != nil {
		return err
	}
	env, err := loadEnv()
	if err != nil {
		return err
	}
	run, err := loadRun(*path)
	if err != nil {
		return err
	}
	describe(run, parseRule(*score, 0.5, *check))
	fmt.Println("| t | complete (all positive) | gold | confusable | near-dup kept | v2 | leak (all negative) | near-miss leak | kept chunks/goal |")
	fmt.Println("|---:|---|---:|---:|---:|---:|---|---:|---:|")
	for _, t := range []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.75, 0.8, 0.9} {
		rule := parseRule(*score, t, *check)
		m := env.collect(run, rule, *half)
		count := func(grps []string, fn func(Outcome) bool) (int, int) {
			k, n := 0, 0
			for _, g := range m {
				for _, grp := range grps {
					if group(g.Goal) == grp {
						n++
						if majority(g.Outs, fn) {
							k++
						}
					}
				}
			}
			return k, n
		}
		pk, pn := count([]string{"gold", "confusable", "v2 single", "v2 collect", "v2 comparison"}, isComplete)
		gk, gn := count([]string{"gold"}, isComplete)
		ck, cn := count([]string{"confusable"}, isComplete)
		ok, _ := count([]string{"confusable"}, isOther)
		vk, vn := count([]string{"v2 single", "v2 collect", "v2 comparison"}, isComplete)
		lk, ln := count([]string{"near-miss", "distractor"}, isLeak)
		nk, nn := count([]string{"near-miss"}, isLeak)
		var kept []float64
		for _, g := range m {
			if !g.Goal.Negative() {
				for _, o := range g.Outs {
					kept = append(kept, float64(o.Kept))
				}
			}
		}
		fmt.Printf("| %.2f | %s | %d/%d | %d/%d | %d/%d | %d/%d | %s | %d/%d | %.2f |\n", t, kn(pk, pn), gk, gn, ck, cn, ok, cn, vk, vn, kn(lk, ln), nk, nn, mean(kept))
	}
	fmt.Println()
	fmt.Println("Kept chunks/goal counts positive goals only.")
	return nil
}

func cmdCompare(args []string) error {
	f := flag.NewFlagSet("compare", flag.ContinueOnError)
	pa := f.String("a", "", "archive A")
	pb := f.String("b", "", "archive B")
	sa := f.String("score-a", "p", "rule for A")
	sb := f.String("score-b", "p", "rule for B")
	ta := f.Float64("t-a", 0.5, "threshold for A")
	tb := f.Float64("t-b", 0.5, "threshold for B")
	half := f.String("half", "all", "dev, holdout, or all")
	if err := f.Parse(args); err != nil {
		return err
	}
	env, err := loadEnv()
	if err != nil {
		return err
	}
	ra, err := loadRun(*pa)
	if err != nil {
		return err
	}
	rb, err := loadRun(*pb)
	if err != nil {
		return err
	}
	ruleA, ruleB := parseRule(*sa, *ta, false), parseRule(*sb, *tb, false)
	ma, mb := env.collect(ra, ruleA, *half), env.collect(rb, ruleB, *half)
	fmt.Printf("A = `%s` (%s), B = `%s` (%s), goals in both, complete or leak in most draws, exact McNemar.\n\n", ra.H.Run, ruleA, rb.H.Run, ruleB)
	fmt.Println("| goals | n | A | B | A only | B only | p | A kept/goal | B kept/goal | A kept chars/goal | B kept chars/goal | A tokens/goal | B tokens/goal |")
	fmt.Println("|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, grp := range rowGroups {
		var k1, k2, b, c, n int
		var kept1, kept2, ch1, ch2, tok1, tok2 []float64
		for id, ga := range ma {
			gb, ok := mb[id]
			if !ok {
				continue
			}
			g := ga.Goal
			switch grp {
			case "all positive":
				if g.Negative() {
					continue
				}
			case "all negative":
				if !g.Negative() {
					continue
				}
			default:
				if group(g) != grp {
					continue
				}
			}
			fn := isComplete
			if g.Negative() {
				fn = isLeak
			}
			x, y := majority(ga.Outs, fn), majority(gb.Outs, fn)
			n++
			if x {
				k1++
			}
			if y {
				k2++
			}
			if x && !y {
				b++
			}
			if y && !x {
				c++
			}
			for i, o := range ga.Outs {
				kept1 = append(kept1, float64(o.Kept))
				ch1 = append(ch1, float64(o.KeptChars))
				tok1 = append(tok1, float64(ga.Recs[i].InputTokens))
			}
			for i, o := range gb.Outs {
				kept2 = append(kept2, float64(o.Kept))
				ch2 = append(ch2, float64(o.KeptChars))
				tok2 = append(tok2, float64(gb.Recs[i].InputTokens))
			}
		}
		if n == 0 {
			continue
		}
		label := grp
		if grp == "near-miss" || grp == "distractor" || grp == "all negative" {
			label += " (leak)"
		}
		fmt.Printf("| %s | %d | %s | %s | %d | %d | %.3g | %.2f | %.2f | %s | %s | %s | %s |\n", label, n, kn(k1, n), kn(k2, n), b, c, mcnemar(b, c), mean(kept1), mean(kept2), commas(mean(ch1)), commas(mean(ch2)), commas(mean(tok1)), commas(mean(tok2)))
	}
	fmt.Println()
	return nil
}

func commas(x float64) string {
	if math.IsNaN(x) {
		return "–"
	}
	s := fmt.Sprintf("%.0f", x)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// cmdInjection reports, for a run over a corpus with planted lines
// (evalinject), how often a planted chunk is kept with and without the
// injection check, and how many clean chunks the check flags.
func cmdInjection(args []string) error {
	f := flag.NewFlagSet("injection", flag.ContinueOnError)
	t := f.Float64("t", 0.5, "keep threshold")
	snippets := f.String("snippets", "evals/injection/snippets.json", "planted lines")
	if err := f.Parse(args); err != nil {
		return err
	}
	lines, err := loadSnippets(*snippets)
	if err != nil {
		return err
	}
	env, err := loadEnv()
	if err != nil {
		return err
	}
	fmt.Println("| run | goal-draws | planted chunk kept, no check | planted chunk kept, with check | planted chunks flagged | clean chunks flagged | positive goals complete, no check → with check |")
	fmt.Println("|---|---:|---|---|---|---|---|")
	for _, path := range f.Args() {
		run, err := loadRun(path)
		if err != nil {
			return err
		}
		var n, keptNo, keptYes, planted, plantedFlagged, clean, cleanFlagged, pos, compNo, compYes int
		for _, rec := range run.Recs {
			g := env.Goals[rec.Goal]
			hasPlant, kn0, kn1 := false, false, false
			for _, c := range rec.Chunks {
				inj, ok := c.X["injection"]
				if !ok {
					continue
				}
				if isPlanted(c.Text, lines) {
					hasPlant = true
					planted++
					if inj >= 0.5 {
						plantedFlagged++
					}
					if c.P != nil && *c.P >= *t {
						kn0 = true
						if inj < 0.5 {
							kn1 = true
						}
					}
				} else {
					clean++
					if inj >= 0.5 {
						cleanFlagged++
					}
				}
			}
			if hasPlant {
				n++
				if kn0 {
					keptNo++
				}
				if kn1 {
					keptYes++
				}
			}
			if !g.Negative() {
				pos++
				if env.outcome(rec, Rule{Mode: "p", T: *t}).Complete {
					compNo++
				}
				if env.outcome(rec, Rule{Mode: "p", T: *t, Check: true}).Complete {
					compYes++
				}
			}
		}
		fmt.Printf("| %s | %d | %s | %s | %s | %s | %d → %d of %d |\n", run.H.Run, n, kn(keptNo, n), kn(keptYes, n), kn(plantedFlagged, planted), kn(cleanFlagged, clean), compNo, compYes, pos)
	}
	fmt.Println()
	return nil
}
