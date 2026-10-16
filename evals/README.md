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
  delightfully meta). All are English: no figure here covers other
  languages.
- `gold/` — the frozen goal sets, the chunk catalog
  (`cmd/chunkcorpus` regenerates `catalog.json` deterministically from
  `corpus/raw/` with skimmer's own chunker), and `split.json`, the frozen
  dev/holdout split by document.
- `PREREG.md` — what is tuned on dev, what is compared on holdout, and
  what decides a change, written before the 0.1.103 runs.
- `variants/prompts/` — candidate question designs, as prompt files.
- `injection/snippets.json` — the lines planted to test `--injection-check`.
- `skimmer/` — **eval history, one folder per skimmer version**. Each
  folder holds the archived runs and `results.md`. From 0.1.103 on,
  `runs/*.jsonl` holds every chunk's span and probability per goal and
  draw.

## Goal kinds

- `gold` (v1, `goals.json`) — a paraphrased reading goal with a known gold
  chunk; authored per selected chunk (so it may share the chunk's words;
  the BM25 baseline measures how much that helps), audited by a second
  model family, adjudicated mechanically (the decisive fact must appear
  in the gold chunk).
- `confusable` (v1) — goal answerable only by one member of a mechanically
  found near-duplicate chunk pair (Jaccard ≥ 0.30 on content words)
  within a single document. The hard discrimination case.
- `distractor` (v1) — a far-off-topic goal no document answers.
- `single`, `collect`, `comparison` (v2, `goals-v2.json`) — goals written
  from document openings before any chunk was seen; gold chunks marked
  by a second model family. Each listed gold chunk is a region the goal
  needs.
- `nearmiss` (`nearmiss.json`) — on the document's subject, asking for a
  fact it does not contain (absence checked by grep of answer terms).

Language models wrote every goal. Every label was then re-checked by
`gpt-6-astra`, a different model family, via `codex exec`
(`go run ./evals/cmd/labelaudit`, results in `gold/audit_astra.json`): it
agrees with all v1 gold, distractor and near-miss labels, 13 of 14
confusables, and only 12 of 36 v2 labels. `evalreport -agreed
gold/audit_astra.json` scores only the agreed goals. No person has
checked a label.

## Scoring

A chunk's place is its span in the document with whitespace removed
(`internal/kit`), so scores hold across chunk sizes and boundary shifts.
A goal's gold region is the span of its gold chunk in the 2000-character
catalog; for a confusable goal, minus what it shares with the
near-duplicate. A region is kept when one kept chunk holds half of it,
when half of one kept chunk lies inside it, or when the kept chunks
together cover half of it. A neighbor sharing only the 10% overlap does
not count. At 2000 characters this reproduces the 0.1.101 id-matched
counts exactly.

A goal is **complete** when it keeps a gold region (v1; v2 needs every
region), and a negative goal **leaks** when it keeps anything. Over
draws, a goal counts when it holds in most of them. Counts are k of n
goals with 95% Wilson intervals; paired comparisons use the exact
McNemar test.

## Commands

Everything live is explicitly invoked. Nothing in `go test` calls Jev.

```sh
go run ./evals/cmd/chunkcorpus > evals/gold/catalog.json   # offline, deterministic
go run ./evals/cmd/evalinject                             # offline: corpora with planted lines

# live: archive every chunk's probability (threshold 0) for a set of goals
SKIMMER_API_KEY=... go run ./evals/cmd/evalskim -model jev-1.14.0 \
    -half all -draws 5 -out evals/skimmer/0.1.103/runs/all-shipped.jsonl
#   -half dev|holdout|all   -sets v1,v2,nearmiss   -kinds gold,...
#   -prompt file.md   -extra name=file.md,injection=   -chunk-chars N   -shift N
#   -corpus evals/injection/<id>   -keep-text

# offline: score archives
go run ./evals/cmd/evalreport summary -run RUN.jsonl [-score p|weighted:W|gate:G] [-t 0.5] [-check]
go run ./evals/cmd/evalreport sweep   -run RUN.jsonl -half dev
go run ./evals/cmd/evalreport compare -a A.jsonl -b B.jsonl [-t-b 0.4] -half holdout
go run ./evals/cmd/evalreport injection evals/skimmer/0.1.103/runs/inject-*.jsonl
```

The model must be a versioned ID (`jev-1.14.0`); aliases are refused.
`evalskim` and `evalreport` replace 0.1.101's `evalshift` and
`evalvariants` (in git history), which scored by chunk id and, for
variants, bypassed the real pipeline.
