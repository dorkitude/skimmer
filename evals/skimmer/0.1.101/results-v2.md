# Skimmer 0.1.101 — harder eval (goals-v2), ranked metrics

Pinned `jev-1.14.0`, 2 draws, 40 off-frame goals (21 single, 14 collect,
5 comparison) over the same 12-document corpus, single-source mode,
ranked by per-chunk probability. Goals authored from document *openings
only* (before any chunk was seen — no in-frame authoring shortcut), then
gold-marked by a second model family (grok-4.5) reading the full
documents; two goals the marker returned empty for were verified
unanswerable-in-extract and dropped (the Time Machine extract has no
Eloi/Morlock explanation; the RFC 9110 extract stops before section 9's
method-safety table). Raw scores: `runs-v2.jsonl` (threshold 0).

This is the eval with headroom: gold rank is measured, not just
kept/dropped.

## Recall@k and all-modes coverage (pooled across 2 draws)

| kind | n (per draw) | R@1 | R@3 | R@5 | R@10 | all-modes@10 |
|---|---:|---:|---:|---:|---:|---:|
| single | 38 | 0.632 | 0.763 | 0.895 | 1.000 | 0.947 |
| collect | 28 | 0.714 | 1.000 | 1.000 | 1.000 | 0.786 |
| comparison | 10 | 0.600 | 0.800 | 0.800 | 0.800 | 0.600 |
| ALL | 76 | 0.658 | 0.855 | 0.921 | 0.974 | 0.842 |

## What the misses are

- **Mode drops are real on collect goals** (Tiff's fourth concern, now
  measured): 6/28 goal-draws lose a gold region from the top 10. The
  pattern in `runs-v2.jsonl`: the missing regions score 0.5-0.7 while
  adjacent near-miss chunks score 0.8+ — collect goals need *every*
  instance, and a single yes/no relevance question under-scores the
  second-and-later instances of a repeated thing. h012 is the clearest
  case: 5 of 7 gold chunks rank top-10, the last two sit at ranks 11-12.
- **Comparison goals are the weakest shape** (R@1 0.60): the two facts
  live in different chunks and only one scores high; the model keeps the
  more "relevant-looking" side. A two-sided question asked one chunk at
  a time is structurally mismatched.
- **Single goals keep R@10 = 1.000** — the skim finds the fact; it just
  doesn't always *rank* it first (R@1 0.63).

## Verdict

The hard eval agrees with the first one on the production property
(find-the-fact) and exposes exactly where a single-noul-per-chunk design
has headroom: ranked precision and complete coverage. Two measured
candidates for a future version: a collect-aware prompt (ask "is this
one instance of the things to collect" rather than generic relevance),
and ranking by a second focused question on the shortlist rather than
raw relevance probability.
