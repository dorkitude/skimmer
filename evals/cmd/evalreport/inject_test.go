package main

import "testing"

// Mixed chunks must contribute their original content before the check drops
// them. Otherwise missing spans make both outcomes fail and hide answer loss.
func TestInjectionArchivesCountLostAnswers(t *testing.T) {
	t.Chdir("../../..")
	env, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		before, after int
	}{
		{"relevance", 65, 62},
		{"override", 64, 60},
		{"editorial", 64, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, err := loadRun("evals/skimmer/0.1.103/runs/inject-" + tc.name + ".jsonl")
			if err != nil {
				t.Fatal(err)
			}
			before, after, positives := 0, 0, 0
			for _, rec := range run.Recs {
				for _, c := range rec.Chunks {
					if c.Span == nil {
						t.Fatalf("missing span for %s, %s#%d", rec.Goal, c.Doc, c.I)
					}
				}
				if env.Goals[rec.Goal].Negative() {
					continue
				}
				positives++
				if env.outcome(rec, Rule{Mode: "p", T: 0.5}).Complete {
					before++
				}
				if env.outcome(rec, Rule{Mode: "p", T: 0.5, Check: true}).Complete {
					after++
				}
			}
			if positives != 66 || before != tc.before || after != tc.after {
				t.Fatalf("complete %d → %d of %d; want %d → %d of 66", before, after, positives, tc.before, tc.after)
			}
		})
	}
}
