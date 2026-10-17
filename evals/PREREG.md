# Pre-registration: the 0.1.103 eval round

Written 2026-10-16, before any run in this round, in answer to
[issue #3](https://github.com/dorkitude/skimmer/issues/3). It fixes what is
tuned where, what is compared, and what decides a change, so the holdout
numbers are not picked after the fact.

## Data

- **Split** (`gold/split.json`): 6 dev and 6 holdout documents; goals follow
  their document, distractors are split by id. Tuning reads dev only. Each
  holdout comparison below is run once.
- **Goal sets:** v1 (`goals.json`: gold, confusable, distractor), v2
  (`goals-v2.json`: single, collect, comparison), and near-miss
  (`nearmiss.json`: on-topic goals the document does not answer).
- **Model:** `jev-1.14.0`, pinned. All runs record the model that answered.
- **Scoring:** by document span (`internal/kit`), so it holds across chunk
  sizes and boundary shifts. At 2000 characters it reproduces the 0.1.101
  id-matched counts exactly.

## Outcomes

Per goal, at the configuration's keep threshold:

- **complete** (primary): v1 gold goals keep a gold region; confusable goals
  keep the gold region; v2 goals keep *every* gold region.
- **leak**: a distractor or near-miss goal keeps any chunk.
- **kept**: chunks kept per goal; **tokens**: Jev input tokens per goal.
- Ranked metrics (R@k, all-modes@10) are reported, but they decide nothing.

Over draws, a goal counts as complete when it is complete in most draws
(ties count as not complete). Counts are reported as k of n goals with 95%
Wilson intervals; paired comparisons use the exact McNemar test.

## Question design (issue #3, S1, S3, S4)

Candidates, each a prompt set in `variants/prompts/`, all run through the
real pipeline (batches of up to 24 chunks, the source as `context`):

- `nosource`: the shipped prompt without the "The document is {{.Source}}" sentence (S4).
- `criteria`: a short question, with the yes and no boundaries moved into Noul criteria (S3).
- `split`: two questions per chunk, "is it on the goal's subject?" and "does
  it carry something the reader would use?", combined in code (S1) as
  - `weighted:w`: w·subject + (1−w)·substance, w ∈ {0.3, 0.5, 0.7}
  - `gate:g`: keep when subject ≥ g and substance ≥ t, g ∈ {0.3, 0.45, 0.6}

On dev (2 draws), each candidate and its parameters are scored at
thresholds 0.3 to 0.8. The **dev winner** is the candidate with the most
complete goals, then the fewest kept chunks, then the fewest tokens, among
those whose leak is no more than 2 goals above shipped's on dev.

The dev winner alone, with its dev-chosen parameters and threshold, is run
on holdout (5 draws) against the shipped prompt at 0.5 (5 draws). It
replaces the shipped prompt only if, on holdout:

1. it is complete on more goals with exact McNemar p < 0.05; or
2. it is non-inferior (complete on no more than 1 fewer goal, a margin set
   here), keeps at least 15% fewer chunks per goal, leaks on no more than
   1 more goal, and costs no more than 10% more tokens.

Otherwise the shipped prompt stays, and the results say so.

## Threshold advice

On dev, for the shipped prompt, the advised threshold is the highest one
whose complete count equals that of 0.5. It is then checked on holdout,
and the README gives the holdout result. The default stays 0.5 unless the
holdout loses no complete goal at the advised threshold.

## Chunk size

Swept on dev at 1000, 2000, 3000 and 4000 characters (2 draws, positive
goals). The default stays 2000 unless another size is non-inferior on dev
(within 1 complete goal) and at least 15% cheaper in tokens.

## Robustness and baselines (reported, decide nothing)

- **Draws (S10):** the final configuration, 5 draws over all goals; chunks
  that cross the threshold between draws, per draw pair.
- **Offsets (S10):** shifts of 50, 100, 200 and 500 runes, 2 draws.
- **Keyword baseline (S8):** BM25 over the same chunks, keeping as many
  chunks per goal as skimmer kept.
- **Injection check (S5):** three injected lines planted in every document;
  how often the planted chunk is kept with and without `--injection-check`,
  and how many clean chunks the check flags.
