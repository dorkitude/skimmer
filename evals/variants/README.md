# Technique variants, measured head-to-head

> **Corrections (2026-10-16, [issue #3](https://github.com/dorkitude/skimmer/issues/3)).**
> Kept as history; [`../skimmer/0.1.103/results.md`](../skimmer/0.1.103/results.md)
> re-runs the question-design comparison through the real pipeline on
> held-out documents.
>
> - **Token column:** the archived `<variant>-runs.json` files give mean
>   input tokens per goal of 11,602 (base), 9,914 (state), 11,706 (atomic),
>   11,087 (collect) and 12,748 (rerank), identical in both draws. The table's
>   5,053 / 4,105 / 5,113 / 10,341 / 12,245 cannot be reproduced from them.
>   On the archives, state is **14.5%** cheaper than base, not 19%, and
>   collect is not "double tokens".
> - **"Equal-or-better recall at every rank" does not hold:** state's R@10
>   is 0.986 against base's 1.000. Per goal (the prompt that hit in more of
>   its two draws), they differ on at most 2 of 36 goals at each R@k and on
>   6 at all-modes@10, split 3 and 3; exact McNemar p ≥ 0.5 everywhere. This
>   data cannot tell them apart.
> - **The atomic variant multiplied its two answers**, so an on-topic but
>   thin chunk scored near zero. 0.1.103 tests a weighted sum and a gate.
> - **This harness was not the shipped pipeline:** it sent each document in
>   one request, without the source context or batching.

Five ways of asking Jev the same skim question, run on the same frozen
goals-v2 eval (36 off-frame goals, 12 public documents, pinned
`jev-1.14.0`, 2 draws each, single-document mode, ranked by per-chunk
probability). "Tokens per goal" is Jev input tokens for one goal over
one document — the real cost axis, since price is per input token.

## The variants, in plain English

- **base** — the shipped design: every chunk's text is copied into its own
  question, which asks one broad "does this help with the goal?" yes/no.
- **state** — the document's chunks sit in the request's data area once;
  each question just points at one ("does chunk c07 help with the goal?")
  instead of quoting the chunk inside the question. Nothing is sent twice.
- **atomic** — two smaller questions per chunk — "is it about the right
  subject?" and "does it state a specific usable fact?" — and the program
  combines them: a chunk counts as yes only when both answers are high.
- **collect** — like "state", but the question says out loud that the goal
  may want *every* instance of something (every figure, every metric),
  so each chunk carrying one instance counts yes on its own. Aimed at
  the mode-drop misses: goals that need several chunks and only got one.
- **rerank** — two rounds: the base question over everything first, keep
  the top ten, then a second request asking "of these ten, is this one
  the single best for the goal?" for each member, and reorder by that.

## Results (pooled over 2 draws; higher is better except tokens)

| variant | R@1 | R@3 | R@5 | R@10 | all-modes@10 | tokens/goal |
|---|---:|---:|---:|---:|---:|---:|
| base (shipped) | 0.597 | 0.833 | 0.917 | **1.000** | 0.722 | 5,053 |
| **state** | 0.597 | **0.875** | **0.944** | 0.986 | **0.750** | **4,105** |
| atomic | **0.625** | 0.917 | 0.917 | 0.958 | 0.708 | 5,113 |
| collect | 0.653* | 0.833 | 0.903 | 0.944 | 0.722 | 10,341 |
| rerank | 0.597 | 0.847 | 0.917 | 0.972 | 0.694 | 12,245 |

(*collect's R@1 is the best on the kinds it targets — 0.786 on
collect goals — but it pays double tokens and loses elsewhere.)

## What won, and what didn't

- **state is the free win**: 19% cheaper (nothing sent twice) *and*
  slightly better at every rank past 1 and at all-modes coverage
  (0.750 vs 0.722). On the collect goals, where the mode drops live, it
  lifts all-modes coverage from 0.536 to 0.643. This is the one change
  worth making to skimmer's shipped prompt: move the chunk text into
  the state and point the question at it.
- **atomic** helps ranked precision at R@1/R@3 but loses at R@10 and
  coverage — two thresholds compound, and chunks that are on-topic but
  thin get zeroed. Not worth the complexity at 1% more tokens.
- **collect** fixes what it aims at (collect-goal R@1 0.786 vs base
  0.643) but at double the token count, and it hurts the other kinds.
  Only worth it if collect-style goals dominate your use.
- **rerank** does not pay: 2.4x the tokens for no measurable gain. The
  first round's ordering was already close; the second round's "which of
  these is best" question doesn't add information the first round
  didn't have.

Raw per-goal scores for every variant are in `<variant>-runs.json`.
