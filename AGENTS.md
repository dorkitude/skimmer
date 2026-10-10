# AGENTS.md

Guidance for coding agents (Claude Code, Codex, ...) working on skimmer.
`CLAUDE.md` is a symlink to this file.

## Project overview

skimmer is a Go CLI (Cobra + Viper) that saves tokens: it splits pages, files,
or lists into chunks/items, asks [Jev](https://typesafe.ai) one yes/no question
per chunk ("does this help with the goal?"), and prints only what passes. The
chunk filter and Jev client started in [webctl](https://github.com/dorkitude/webctl);
keep the two consistent where it makes sense.

## Build & test

```bash
go build -o skimmer .
go vet ./... && go test ./...
gofmt -l .                      # must print nothing (CI enforces it)
```

Tests never call the real Jev API: `internal/jev/judge_test.go` uses an
`httptest` fake. For a manual end-to-end check, run `./skimmer auth status`
and then a real `skimmer skim ... -g ...`.

## Layout

- `main.go`: entry point
- `cmd/`: Cobra commands (`skim`, `filter`, `auth`, `prompts`) and the version string
- `internal/jev/`: System One client; `judge.go` holds the batching (token
  budget, parallel batches, split-on-too-large, unjudged items kept)
- `internal/chunk/`: line-aware chunker with overlap
- `internal/fetch/`: page fetching and HTML/PDF to text (from webctl)
- `internal/skim/`: the skim pipeline (load, chunk, judge, select)
- `internal/items/`: parsing and re-encoding lists (JSON, JSONL, lines)
- `internal/config/`: precedence of flags, env, `.env`, and `~/.config/skimmer/config.yaml`
- `internal/prompts/` + `prompts/<kind>/*.md`: embedded prompt templates

## Rules

- **Never print or log the API key.** `auth status` shows only `config.Mask`.
  Never commit keys or `.env` files.
- When a chunk or item gets no verdict, keep it. A Jev failure must never
  silently drop content.
- Keep `--json` output stable; agents parse it.
- A new flag or behavior updates the README's Usage section and `CHANGELOG.md`.
- Commits carry no AI attribution (no `Co-Authored-By` trailers or
  "Generated with" lines).

## Release strategy

### Versions

- Semver with large patch numbers, as in cfctl: `0.1.100`, `0.1.101`, ...
  Bump the patch for each release and the minor for big shifts. Never zero-pad
  (`0.1.001` is not valid semver: Go and GoReleaser reject it).
- The tag is the version with a `v`: `0.1.100` → `v0.1.100`.
- The binary learns its version from the tag. GoReleaser and the Homebrew
  formula set `-X github.com/dorkitude/skimmer/cmd.Version=X.Y.Z`, and
  `go install ...@vX.Y.Z` builds read it from the module info. There is no
  version constant to bump.

### Channels

| Channel | Source of truth | Updated by |
|---|---|---|
| GitHub release (macOS/Linux tarballs, Windows zips, .deb, .rpm, checksums) | `.goreleaser.yaml` | `release` workflow, on tag push |
| apt (`dorkitude.github.io/skimmer-apt`, signed) | `scripts/publish-apt.sh` | `release` workflow, on tag push |
| Homebrew (`dorkitude/homebrew-tap`, `Formula/skimmer.rb`, built from source) | `scripts/publish-packages.sh` | maintainer, after the release exists |
| Scoop (`dorkitude/scoop-bucket`, `skimmer.json`) | `scripts/publish-packages.sh` | maintainer, after the release exists |
| `go install github.com/dorkitude/skimmer@latest` | the tag | automatic |

The tap and bucket are shared with other dorkitude tools, so they are updated
from the maintainer's machine with their own `gh` login, not from CI.

### Secrets (on dorkitude/skimmer)

- `APT_GPG_PRIVATE_KEY`: the apt repo signing key (RSA 4096, fingerprint
  `241D677482BEC395E187B4693D1A4098CB4041F2`). This secret is its only copy.
  If it's lost, generate a new key, set the secret, and tell users to
  re-import `key.gpg`.
- `APT_DEPLOY_KEY`: SSH key for a write deploy key on `dorkitude/skimmer-apt`
  only.

### Cutting a release

Agents create or push tags only when the maintainer explicitly asks for a
release.

1. On `main`, with CI green: move the `CHANGELOG.md` entries from
   `[Unreleased]` into `## [X.Y.Z] - YYYY-MM-DD`, then commit and push.
2. Tag and push:
   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z: short summary"
   git push origin vX.Y.Z
   ```
3. Watch it: `gh run watch -R dorkitude/skimmer`. The workflow runs vet and
   tests, then GoReleaser, then the apt publish.
4. Add the notes: `gh release edit vX.Y.Z -R dorkitude/skimmer --notes "<from CHANGELOG>"`.
5. Publish Homebrew and Scoop: `scripts/publish-packages.sh vX.Y.Z`
   (`DRY_RUN=1` to preview). It's idempotent.
6. Verify each channel reports `X.Y.Z`:
   ```bash
   go install github.com/dorkitude/skimmer@vX.Y.Z && skimmer --version
   brew update && brew install dorkitude/tap/skimmer && skimmer --version     # or brew upgrade
   sudo apt update && sudo apt install skimmer && skimmer --version
   scoop update && scoop install dorkitude/skimmer                            # on Windows
   ```

If the workflow fails after GoReleaser has published, fix the problem, delete
the release and tag (`gh release delete vX.Y.Z --cleanup-tag`), and tag again.
Don't reuse a version once any channel has published it; bump the patch
instead.
