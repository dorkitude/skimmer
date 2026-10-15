# Changelog

All notable changes to skimmer. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions are semver with large patch numbers (`0.1.100`, `0.1.101`, ...).

## [Unreleased]

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
