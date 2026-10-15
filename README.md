# skimmer

Skim web pages, files, and lists with [Jev](https://typesafe.ai), and keep only the parts that help with your goal.

```bash
skimmer skim https://en.wikipedia.org/wiki/Bloom_filter \
  -g "What is the false positive rate formula, and how do I pick the number of hash functions?"
# skimmer: 1 source(s), kept 4/40 chunks, 71k → 7.6k chars (89% saved)
```

skimmer saves tokens. A coding agent (or you) shouldn't have to read a whole page, a pile of grep hits, or 40 search results to find the three paragraphs that matter. Reading less makes each turn cheaper and faster, and it also improves quality, because the model isn't distracted by junk.

It's the chunk filter from [webctl](https://github.com/dorkitude/webctl), pulled out so it works on any material, not just search results.

## How it works

```
  page / file / stdin                       a list (JSON, JSONL, lines)
          │                                            │
          ▼                                            │
  ┌───────────────┐                                    │
  │  ~2,000-char  │  10% overlap,                      │
  │    chunks     │  line ranges remembered            │
  └───────┬───────┘                                    │
          │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │
          ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼
  ┌───────────────────────────────────────────────────────┐  one yes/no question per
  │                          Jev                          │  chunk or item, batched to 
  │       "does this help with the goal?" → P(yes)        │  fit context, run in parallel
  └───────┬───────────┬───────────┬───────────────────────┘
          ▼           ▼           ▼        ✂️  the rest dropped
  ┌───────────────────────────────────────────┐
  │   kept chunks (cited as path:40-58)       │  ✅ only this reaches your context
  │   or kept items (same format as input)    │
  └───────────────────────────────────────────┘
```

When Jev can't judge a chunk (a failed batch, say), skimmer keeps it rather than silently dropping it.

## Install

### Homebrew (macOS and Linux)

```bash
brew install dorkitude/tap/skimmer
```

### apt (Debian and Ubuntu)

```bash
curl -fsSL https://dorkitude.github.io/skimmer-apt/key.gpg | sudo gpg --dearmor -o /usr/share/keyrings/skimmer.gpg
echo "deb [signed-by=/usr/share/keyrings/skimmer.gpg] https://dorkitude.github.io/skimmer-apt stable main" | sudo tee /etc/apt/sources.list.d/skimmer.list
sudo apt update && sudo apt install skimmer
```

### Scoop (Windows)

```powershell
scoop bucket add dorkitude https://github.com/dorkitude/scoop-bucket
scoop install dorkitude/skimmer
```

### go install

```bash
go install github.com/dorkitude/skimmer@latest
```

### Binaries and packages

Every [release](https://github.com/dorkitude/skimmer/releases) has macOS, Linux, and Windows builds (amd64 and arm64), plus `.deb` and `.rpm` packages and `checksums.txt`.

## Auth

Get a Jev key at [typesafe.ai](https://typesafe.ai), then run this once:

```bash
skimmer auth                 # prompts (masked), checks the key, saves ~/.config/skimmer/config.yaml (0600)
skimmer auth status          # where the key comes from, plus a live check
skimmer auth logout
```

Other ways to supply the key, highest precedence first:

1. the `--api-key` flag
2. the `SKIMMER_API_KEY` env var (`JEV_API_KEY` and `TYPESAFE_API_KEY` work too)
3. a `.env` file in the current directory
4. `~/.config/skimmer/config.yaml` (written by `skimmer auth`; honors `$XDG_CONFIG_HOME` and `$SKIMMER_CONFIG_DIR`)

**Behind a credential-injecting proxy** (an [exe.dev integration](https://exe.dev/docs/integrations.md), for example), store the proxy URL and skip the key. Your machine never holds the key:

```bash
skimmer auth --base-url https://typesafe.int.exe.xyz   # press Enter at the key prompt
```

## Usage

### Skim pages and files

```bash
# Web pages (HTML, PDFs, Reddit threads)
skimmer skim https://go.dev/doc/effective_go -g "how should I name interfaces?"

# Files and directories (directories are walked; .git, node_modules, and hidden dirs are skipped)
skimmer skim ./docs README.md -g "how do I configure retries?"

# Grep hits: pipe `rg -l`, `rg -n`, `grep -rn`, or `find` output and skimmer reads the files
rg -n "TODO" . | skimmer skim -g "TODOs about auth or security"

# Any text on stdin
pbpaste | skimmer skim -g "action items assigned to Kyle"
```

File chunks are cited as `path:start-end`, so you (or your agent) can jump straight to them:

```
## ./internal/provider/cooldown.go: kept 2 of 4 chunks (3.5k of 6.5k chars)

[./internal/provider/cooldown.go:157-202 · p=0.93]
func (c *Cooldown) Note(name string, err error) {
...

## nothing relevant in 12 source(s): ./cmd/webctl/cli/search.go, ...
```

| Flag | Meaning |
|---|---|
| `-g, --goal` | what you're trying to accomplish (required) |
| `-t, --threshold` | keep chunks with P(relevant) ≥ this (default 0.5) |
| `-n, --max-chunks` | keep at most N chunks per source, best first |
| `--chunk-chars` | chunk size (default 2000) |
| `--overlap` | fraction of each chunk repeated from the previous one, so text straddling a boundary is seen whole (default 0.1; `--overlap 0.2` for more, `--overlap 0` to disable; max 0.5) |
| `--max-chars` | read at most this many characters per source (default 400k) |
| `--json` / `--raw` | structured output / just the kept text |
| `--paths` / `--text` | force stdin to be read as a path list / as the document |
| `-p, --prompt` | a custom prompt (see below) |
| `-q, --quiet` | no savings summary on stderr |

### Filter lists

```bash
gh search repos "bloom filter" --json fullName,description,language --limit 40 \
  | skimmer filter -g "a production-quality Bloom filter library for Go"
# skimmer: kept 3 of 40 items (json)

curl -s "$SEARCH_API?q=..." | skimmer filter --key results --fields title,snippet -g "tutorials with code"
ls ~/notes | skimmer filter -g "anything about the Q3 offsite"
skimmer filter results.jsonl -g "papers from 2025 on sparse autoencoders" --scores
```

skimmer reads a JSON array, an object holding one array (choose among several with `--key`), JSON Lines, or plain lines, and detects which one it got. Kept items come back untouched, in their original order, in the same format. `--fields` shows Jev only those fields of each object. `--scores` adds each item's index and P(relevant), and `-n/--max` keeps only the top N.

## Prompts

Each chunk or item gets one yes/no Jev question, rendered from a template. The defaults are [`prompts/skim_document/DEFAULT.md`](prompts/skim_document/DEFAULT.md) and [`prompts/filter_results/DEFAULT.md`](prompts/filter_results/DEFAULT.md).

```bash
skimmer prompts list
skimmer prompts show skim_document
skimmer prompts init skim_document strict   # copies the default to ~/.config/skimmer/prompts/skim_document/strict.md
skimmer skim ./src -g "..." --prompt strict
skimmer skim ./src -g "..." --prompt ./my-prompt.md
```

A prompt is optional YAML frontmatter (`name`, `description`, `model`) followed by a Go `text/template` body. The body can use `{{.Goal}}`, `{{.Text}}`, `{{.ID}}`, `{{.Index}}`, and, for skim, `{{.Source}}` and `{{.Lines}}`. A user prompt named `DEFAULT.md` replaces the built-in default. The built-in defaults send each chunk once in the request state and point the question at it (`{{.ID}}`) rather than pasting the text into the question; that measured 19% cheaper with equal-or-better accuracy (see [evals/variants/](evals/variants/README.md)).

## For agents

Paste this into your `CLAUDE.md` / `AGENTS.md`:

```markdown
## Reading large material: use skimmer
Before reading a long web page, a big file, many grep hits, or a long list of results,
run it through `skimmer` with a specific goal, and read only what comes back:
- `skimmer skim <url|file|dir>... -g "<what you need>"`
- `rg -n "<pattern>" . | skimmer skim -g "<what you need>"`  (reads the matching files)
- `<command emitting JSON/lines> | skimmer filter -g "<what you need>"`
Kept file chunks are cited as path:start-end; open the file there if you need more context.
If skimmer reports nothing relevant, rephrase the goal or lower `-t` before reading raw.
```

## Evals

The skim filter is measured, not vibes: [`evals/`](evals/README.md) holds a
public-document corpus (with attributions), a gold-by-construction goal set
(52 gold + 14 in-doc near-duplicate confusables + 12 distractors), and a live
harness that runs the real pipeline against a pinned Jev model and archives
every per-chunk probability, with history foldered by skimmer version
(`evals/skimmer/0.1.101/`). Measured on `jev-1.14.0`: recall 1.000, distractor
leak 0.000, stable across draws, robust to boundary shifts; threshold 0.5 is
conservative (0.7 keeps 30% fewer chunks at identical recall), and 2000-char
chunks are the measured sweet spot. Nothing in `go test` makes a live call.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The release process is in [AGENTS.md](AGENTS.md#release-strategy), and changes are listed in [CHANGELOG.md](CHANGELOG.md).

## License

MIT
