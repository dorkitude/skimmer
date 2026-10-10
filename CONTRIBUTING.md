# Contributing to skimmer

Thanks for helping! skimmer exists to save tokens, so changes are judged by
whether they help an agent read less without missing what matters.

## Development

You need Go 1.24+ and `git`. Tests don't need a Jev key: they run against a
fake API.

```bash
git clone https://github.com/dorkitude/skimmer.git
cd skimmer
go build -o skimmer . && ./skimmer --help
go vet ./... && go test ./...
gofmt -l .        # must print nothing
```

For manual end-to-end runs, set up a key once (`./skimmer auth`, see the
README), then try something real:

```bash
./skimmer skim https://en.wikipedia.org/wiki/Bloom_filter -g "false positive rate formula"
```

The stderr summary (`kept 4/45 chunks, 71k → 7.6k chars`) is the number to
watch when you change chunking, prompts, or thresholds.

## Where things live

See the layout in [AGENTS.md](AGENTS.md#layout). Most contributions touch one of:

- **Prompts** (`prompts/skim_document/`, `prompts/filter_results/`): the
  biggest quality lever. Changing a `DEFAULT.md` changes behavior for
  everyone, so include before/after runs (chunks kept, and whether the right
  ones survived) on at least a web page, a code search, and a list.
- **Chunking** (`internal/chunk/`): keep chunks within `--chunk-chars`
  (overlap included) and keep line numbers exact. They're how agents cite
  results.
- **Batching** (`internal/jev/judge.go`): keep the guarantees: requests stay
  under Jev's context, batches run in parallel, an oversized batch is split
  and retried, and unjudged items are kept, never dropped.
- **Input/output** (`cmd/`, `internal/items/`): keep `--json` stable, keep
  kept list items byte-for-byte the same as their input, and keep stdout to
  results only (summaries go to stderr).

## Rules

- Never print, log, or commit API keys.
- New behavior comes with tests. Network-facing code is tested against
  `httptest` fakes, never the real Jev API.
- A new flag or behavior updates the README Usage section and gets a line
  under `## [Unreleased]` in `CHANGELOG.md` (Added / Changed / Fixed / Docs).
- Keep commits focused. Conventional prefixes (`feat:`, `fix:`, `docs:`,
  `test:`, `chore:`) are welcome.

## Pull requests

1. Fork, branch, and make your change with tests.
2. Run `go vet ./... && go test ./... && gofmt -l .`.
3. Open a PR saying what changed and why. For prompt or chunking changes,
   include the before/after numbers.

CI runs the same checks plus `goreleaser check`.

## Releases

Only the maintainer releases. The process is in
[AGENTS.md](AGENTS.md#release-strategy).
