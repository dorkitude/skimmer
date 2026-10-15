# evals

Live measurements of the skimmer itself, following the methodology of
[dorkitude/system-one-techniques](https://github.com/dorkitude/system-one-techniques)
(gold by construction, blind audit by a second model family, per-draw draws,
archived raw scores, miss attribution).

Layout:

- `corpus/` — the public-document corpus and its attributions
  (`corpus/SOURCES.md`). Twelve documents, all permitted for
  academic/noncommercial use; three of the arXiv papers are Kyle Wild's
  RAG papers (mutually confusable, which is deliberate), and one is the
  System One paper that grounded the techniques repo (kept because it is
  delightfully meta).
- `gold/` — the frozen goal set and the chunk catalog
  (`cmd/chunkcorpus` regenerates `catalog.json` deterministically from
  `corpus/raw/` with skimmer's own chunker).
- `skimmer/` — **eval history, one folder per skimmer version**. Each
  folder holds `runs.jsonl` (archived raw per-chunk scores per goal) and
  `results.md` (the measured summary).

## Goal kinds

- `gold` — a paraphrased reading goal with a known gold chunk; authored
  per selected chunk, audited by a second model family, adjudicated
  mechanically (the decisive fact must appear in the gold chunk).
- `confusable` — goal answerable only by one member of a mechanically
  found near-duplicate chunk pair (Jaccard ≥ 0.30 on content words)
  within a single document. The hard discrimination case.
- `distractor` — a goal no document answers; the claim under test is
  "nothing here helps."

## Commands

Everything live is explicitly invoked. Nothing in `go test` calls Jev.

```sh
go run ./evals/cmd/chunkcorpus > evals/gold/catalog.json   # offline, deterministic
SKIMMER_BASE_URL=... go run ./evals/cmd/evalskim \
    -model jev-1.14.0 -draws 2 -skimmer-version 0.1.101 -threshold 0
go run ./evals/cmd/evalshift -model jev-1.14.0 ...         # boundary sensitivity
```

`-threshold 0` archives every per-chunk probability, which is what the
offline threshold sweeps consume. The pinned model is a versioned ID
(`jev-1.14.0`), never an alias.

## Metrics

Per draw: recall (any gold chunk kept), confusable gold/other keep rates,
distractor leakage (any chunk kept for a distractor goal), mean kept
chunks per goal, input tokens per goal, chunks judged per goal, and the
cross-draw score delta. Threshold sweeps replay archived scores offline.
