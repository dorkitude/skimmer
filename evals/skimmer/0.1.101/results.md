# Skimmer 0.1.101 — measured

> **Corrections (2026-10-16, [issue #3](https://github.com/dorkitude/skimmer/issues/3)).**
> These figures are kept as history; [`../0.1.103/results.md`](../0.1.103/results.md)
> supersedes them.
>
> - **The chunk-size sweep's confusable columns were not measured.** The
>   harness scored confusables by chunk id (`doc-NN`), and at sizes other
>   than 2000 the same id names different text. Re-scored by document span,
>   smaller chunks do *not* lose discrimination (0.1.103 results). "Do not
>   shrink chunks to save tokens — it costs quality" is withdrawn.
> - **Headline counts are small.** "Recall 1.000" is 52 of 52 goals (95%
>   Wilson 0.93–1.00); "distractor leak 0.000" is 0 of 12 far-off-topic
>   goals (0.00–0.24); confusable gold kept is 14 of 14 (0.78–1.00).
> - **The threshold advice (0.7) was picked on the goals that report it.**
>   It does not hold for the 0.1.102 prompt on held-out documents.
> - **Token figures:** 53,786 is the mean over all 78 goals in `runs.jsonl`,
>   including 12 distractors judged over all 236 chunks (239,200 tokens
>   each); gold goals average 19,577 and confusables 21,923. The 16,986
>   in the baseline table matches no goal kind in the archived run and
>   could not be reproduced.
> - **Cross-draw delta** came from 2 draws with no committed script; in
>   `runs.jsonl` 15 chunks in 13 goals land on opposite sides of 0.5.

Pinned model `jev-1.14.0`, 2 draws, 78 goals (52 gold, 14 confusable,
12 distractor) over 12 documents / 236 chunks at the default design
(2000-char chunks, 0.10 overlap, `prompts/skim_document/DEFAULT.md`,
single-source mode: each goal judged over its own document).
Fingerprint `a078854764ab2b89305519329beb1dc6`. Raw scores:
`runs.jsonl` (threshold-0, i.e. every chunk's probability) and
`runs-shift.jsonl` (boundary-shift run).

## Baseline (threshold 0.5, the shipped default)

Pooled across both draws:

| metric | value |
|---|---:|
| gold recall | **1.000** (52/52 per draw) |
| confusable gold kept | 1.000 (14/14 per draw) |
| confusable other (near-duplicate) kept | 0.214 |
| distractor leak | **0.000** (never keeps a chunk for an unanswerable goal) |
| mean kept chunks per goal | 2.72 |
| input tokens per goal | 16,986 (single-source) |
| cross-draw score delta | mean 0.003, max 0.09 |

The near-duplicate "other" chunks that are kept score high (0.9+) —
skimmer keeps both members and loses nothing; the cost is a noisier
output, not a missed fact. Given the project rule (keep when in doubt),
that is the right failure direction.

## Threshold sweep (replayed offline from the archived threshold-0 scores)

| t | gold recall | confusable gold kept | confusable other kept | distractor leak | mean kept |
|---|---:|---:|---:|---:|---:|
| 0.00 | 1.000 | 1.000 | 1.000 | 1.000 | 53.83 |
| 0.10 | 1.000 | 1.000 | 0.821 | 0.000 | 9.51 |
| 0.20 | 1.000 | 1.000 | 0.571 | 0.000 | 5.46 |
| 0.30 | 1.000 | 1.000 | 0.429 | 0.000 | 3.94 |
| 0.40 | 1.000 | 1.000 | 0.321 | 0.000 | 3.21 |
| 0.50 | 1.000 | 1.000 | 0.214 | 0.000 | 2.72 |
| 0.60 | 1.000 | 1.000 | 0.214 | 0.000 | 2.26 |
| 0.70 | 1.000 | 1.000 | 0.107 | 0.000 | 1.93 |
| 0.75 | 1.000 | 1.000 | 0.071 | 0.000 | 1.69 |
| 0.80 | 1.000 | 1.000 | 0.071 | 0.000 | 1.53 |
| 0.90 | 0.942 | 1.000 | 0.000 | 0.000 | 1.19 |
Recall holds at 1.000 through threshold 0.8; distractor leakage is zero
from 0.1 up. **Threshold 0.5 is conservative**: 0.7 keeps 30% fewer
chunks (1.93 vs 2.72 per goal) at identical recall and distractor
cleanliness, and halves the near-duplicate leakage (0.107 vs 0.214).

## Chunk-size sweep (live, 2 draws, fact-matched)

| chunk-chars | chunks judged/goal | tokens/goal | gold recall | confusable gold | confusable other |
|---:|---:|---:|---:|---:|---:|
| 1000 | 108.2 | 67,056 | 0.962* | 0.14 | 0.07 |
| 2000 (default) | 53.8 | 53,786 | 0.962* | 1.00 | 0.21 |
| 3000 | 35.3 | 49,476 | 0.962* | 0.00 | 0.14 |
| 4000 | 27.1 | 47,709 | 0.962* | 0.01–0.14 | 0.14 |

*The two "misses" at every size are g005/g016, whose decisive facts are
12–14 normalized characters and fall below the 16-char content-match
probe — a matcher artifact, not a model miss (chunk-id matching is
52/52).

The cost curve is nearly flat past 2000 (2000→4000 saves only 11%
tokens) while confusable discrimination collapses above 2000 (1.00 →
0.00). The 2000-char default is at the sweet spot; smaller chunks cost
25% more tokens AND destroy discrimination (0.14). **Do not shrink
chunks to save tokens — it costs quality.**

## Boundary sensitivity (shift run, `runs-shift.jsonl`)

Every document shifted by the full default overlap (200 runes), moving
every chunk boundary. Threshold 0.5, chunk-id gold matched by content:
52 gold goals, 50 kept — both misses are the same two matcher-artifact
goals (g005, g016). Distractor leak 0/12. **Boundary shifts do not
materially move recall** — the 10% overlap does its job.

## Verdicts

1. **The shipped design is sound on quality**: perfect recall, zero
   distractor leakage, stable across draws, robust to boundary shifts.
   Tiff's specific worries did not reproduce on this corpus: no mode
   dropping (every goal's gold region survives), boundary noise is
   absorbed by the overlap, and near-duplicates are kept-both rather
   than conflated.
2. **The improvement is a threshold change, not a redesign.** Threshold
   0.7 (techniques repo's "calibrated threshold" finding, v6-style):
   same recall, same zero leakage, 30% fewer kept chunks, half the
   near-duplicate noise. A one-line change to `DefaultThreshold`.
3. **Chunk size should stay 2000.** Token savings above it are marginal
   and quality (confusable discrimination) falls off a cliff.
