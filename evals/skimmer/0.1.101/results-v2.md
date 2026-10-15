# Skimmer 0.1.101 — harder eval (goals-v2), ranked metrics

Pinned `jev-1.14.0`, 2 draws, 36 off-frame goals (18 single, 14 collect,
4 comparison) over the same 12-document corpus, single-source mode,
ranked by per-chunk probability. Goals authored from document *openings
only* (before any chunk was seen), gold-marked by a second model family
(grok-4.5) reading the full documents. Unanswerable-in-extract goals
(2), marker-unfilled goals whose supposed facts proved absent from the
extracts (4), and pure-table-of-contents gold chunks (h031's TOC
entries) were verified mechanically and dropped. Raw scores:
`runs-v2.jsonl` (threshold 0).

## Recall@k and all-modes coverage (pooled across 2 draws)

| kind | n (per draw) | R@1 | R@3 | R@5 | R@10 | all-modes@10 |
|---|---:|---:|---:|---:|---:|---:|
| single | 32 | 0.594 | 0.750 | 0.875 | 1.000 | 0.938 |
| collect | 28 | 0.750 | 1.000 | 1.000 | 1.000 | 0.821 |
| comparison | 8 | 0.500 | 0.750 | 0.750 | 0.750 | 0.750 |
| ALL | 68 | 0.647 | 0.853 | 0.912 | 0.971 | 0.868 |

## What the misses are

- **Mode drops are real on collect goals** (the fourth concern from
  issue #1, now measured): 5/28 goal-draws lose a gold region from the
  top 10. In the raw scores the missing regions score 0.5-0.7 while
  adjacent near-miss chunks score 0.8+ — a collect goal needs *every*
  instance, and a single generic relevance noul under-scores
  second-and-later instances of a repeated thing.
- **Comparison goals are the weakest shape** (R@1 0.50): the two facts
  live in different chunks and typically only one scores high.
- **Single goals hold R@10 = 1.000** — the skim finds the fact; ranked
  precision is where it loses (R@1 0.59).

## Verdict

The hard eval agrees with the first one on the production property
(find-the-fact: single goals R@10 = 1.000) and exposes where a
single-noul-per-chunk design has headroom: complete coverage on collect
goals and ranked precision. Candidate fixes with baselines to beat: a
collect-aware prompt, and shortlist re-ranking with a second focused
question.
