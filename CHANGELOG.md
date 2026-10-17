# Changelog

All notable changes to skimmer. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions are semver with large patch numbers (`0.1.100`, `0.1.101`, ...).

## [Unreleased]

## [0.1.104] - 2026-10-16

### Changed

- Homebrew installs skimmer as a cask holding the release's prebuilt binary,
  not a formula built from source. On macOS Tahoe Homebrew refuses any
  source build without the Xcode Command Line Tools, so
  `brew install dorkitude/tap/skimmer` failed on Macs without them. The cask
  needs neither Go nor the Command Line Tools, and it clears macOS's
  quarantine flag so the unnotarized binary runs without a warning. If you
  installed the formula, switch once:
  `brew uninstall skimmer && brew install dorkitude/tap/skimmer`.

### Added

- A universal macOS binary in every release (`skimmer_X_darwin_all.tar.gz`,
  Apple Silicon and Intel in one file), which the cask installs. The
  per-architecture macOS, Linux and Windows builds are unchanged.

## [0.1.103] - 2026-10-16

In answer to the review in [#3](https://github.com/dorkitude/skimmer/issues/3).

### Changed

- The default model is pinned to `jev-1.14.0`, the model the evals measured,
  instead of the `jev-latest` alias (which points at `jev-1.13.0`). `--model`,
  `SKIMMER_MODEL` and a prompt's `model:` still choose another.
- The stderr summary and `skim --json` (`"model"`) name the model that
  answered. The summary also counts unjudged chunks.
- The batch planner charges a chunk's text once, or twice only when a prompt
  pastes it with `{{.Text}}`, and measures the prompt itself without
  subtracting text it no longer contains. Batches with large `--chunk-chars`
  now hold up to about twice as many chunks.

### Added

- `--injection-check` for `skim` and `filter`: a second yes/no question per
  chunk or item, in the same requests, drops ones that try to steer the
  reader (listed as `flagged` in `--json`). Measured: planted lines kept
  1–6% of the time instead of 18–20%, 0 flags in 5,439 clean judgments,
  about 25% more input tokens; in documents with a planted line it cost 3–4
  of 66 goals, by dropping answers next to the line. A filter, not a security boundary.
  The prompt is `injection_check/DEFAULT.md`.
- Prompt frontmatter takes `criteria` (`true` and `false` text), sent as the
  Noul's criteria.
- Evals: a frozen dev/holdout split, a pre-registration, near-miss negative
  goals, span-based scoring that holds across chunk sizes and shifts, one
  live runner (`evalskim`) and an offline report (`evalreport`: Wilson
  intervals, exact McNemar, a BM25 baseline, cross-draw flips, injection).
  Results in `evals/skimmer/0.1.103/results.md`.
- A label audit by a second model family (`gpt-6-astra` via `codex exec`,
  `evals/cmd/labelaudit`): it agrees with all v1 gold, distractor and
  near-miss labels and 13 of 14 confusables, but only 12 of 36 v2 labels,
  and every skimmer miss falls on a disputed v2 label. `evalreport -agreed`
  re-scores on agreed goals only; no decision changes.

### Fixed

- Injection eval chunks now map through the planted insertion to their
  original-document spans, including chunks containing only part of the line.
  Repaired the saved spans offline without changing model probabilities:
  the check loses 3–4 complete goals of 66 per planted-line run, previously
  hidden by missing spans. Updated the results and README.
- The chunk-size sweep scored near-duplicate goals by chunk id, which names
  different text at other sizes. Re-scored by span, smaller chunks do not
  lose discrimination; the 0.1.101 claim is withdrawn.
- Eval claims corrected in place: headline counts now carry n and intervals;
  the 0.7 threshold advice is withdrawn (it loses 8 of 42 held-out goals);
  the 0.1.102 prompt change measured 14.5% fewer tokens with no measurable
  recall difference, not "19% fewer, equal-or-better at every rank".

## [0.1.102] - 2026-10-15

### Changed

- Built-in `skim` and `filter` prompts no longer paste the goal or the chunk
  or item text into each question. Every chunk is sent once, in the request
  state, and each question points at it by id. Measured on the frozen
  goals-v2 eval (pinned `jev-1.14.0`, 2 draws): 19% fewer input tokens per
  goal, equal-or-better recall at every rank and better all-modes coverage
  (0.750 vs 0.722). See `evals/variants/README.md`. Custom prompts using
  `{{.Text}}` still work, but send the text twice.

### Added

- Evals for the skim filter itself (`evals/`): a 12-document public corpus with
  attributions, a gold-by-construction goal set (52 gold, 14 mechanically-found
  near-duplicate confusables, 12 distractors) authored and blind-audited across
  two model families, a live harness running the real pipeline against a pinned
  Jev model with every per-chunk probability archived, and eval history
  foldered by skimmer version. Measured on `jev-1.14.0`: recall 1.000,
  distractor leak 0.000, robust to boundary shifts; threshold 0.5 is
  conservative (0.7 keeps 30% fewer chunks at identical recall), and 2000-char
  chunks are the measured sweet spot. No behavior change to the CLI.

## [0.1.100] - 2026-10-10

First release.

### Added

- `skimmer skim`: skims URLs, files, directories, `grep`/`rg` output, or stdin,
  and prints only the chunks Jev judges helpful for `--goal`. File chunks are
  cited as `path:start-end`.
- `skimmer filter`: filters a JSON array, JSON Lines, or plain lines down to the
  helpful items, in the input format. Includes `--fields`, `--key`, `--scores`,
  and `--max`.
- Chunks overlap by 10% by default (`--overlap`).
- `skimmer auth` / `auth status` / `auth logout`: key stored in
  `~/.config/skimmer/config.yaml` (0600). `SKIMMER_API_KEY`, `JEV_API_KEY`, and
  `.env` also work, as does a credential-injecting `--base-url`.
- `skimmer prompts list|show|init`: built-in `DEFAULT` prompts, with user
  overrides in `~/.config/skimmer/prompts/`.
- Install via Homebrew, Scoop, apt, `.deb`/`.rpm`, release binaries, or `go install`.
