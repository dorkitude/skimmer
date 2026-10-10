# Changelog

All notable changes to skimmer. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions are semver with large patch numbers (`0.1.100`, `0.1.101`, ...).

## [Unreleased]

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
