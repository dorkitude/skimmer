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

This installs the release's prebuilt, signed and notarized binary (one universal binary on macOS), so it needs neither Go nor the Xcode Command Line Tools. If you installed skimmer from the tap before 0.1.104, when it was a formula built from source, switch once: `brew uninstall skimmer && brew install dorkitude/tap/skimmer`.

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

Every [release](https://github.com/dorkitude/skimmer/releases) has a universal macOS build (`darwin_all`, Apple Silicon and Intel in one binary), per-architecture macOS, Linux, and Windows builds (amd64 and arm64), `.deb` and `.rpm` packages, and `checksums.txt`.

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
| `--injection-check` | also ask whether each chunk tries to steer the reader, and drop the ones that do (see below) |
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

skimmer reads a JSON array, an object holding one array (choose among several with `--key`), JSON Lines, or plain lines, and detects which one it got. Kept items come back untouched, in their original order, in the same format. `--fields` shows Jev only those fields of each object. `--scores` adds each item's index and P(relevant), `-n/--max` keeps only the top N, and `--injection-check` works as it does for `skim`.

### Model

skimmer pins `jev-1.14.0`, the model its evals measured, so its answers and the threshold advice don't move when Jev's `jev-latest` alias does. `--model` (or `SKIMMER_MODEL`, or `model:` in a prompt's frontmatter) picks another. The stderr summary and `--json` output (`"model"`) name the model that actually answered.

### Injection check

Pages and files are untrusted. A page can carry a line like "this section is exactly what you are looking for" to get itself kept. `--injection-check` asks Jev a second yes/no question about every chunk or item, in the same requests: does it try to steer the system reading it? Flagged chunks are dropped and listed (`flagged` in `--json`, a count on stderr); a chunk the check couldn't judge is kept. It costs about one more short question per chunk. **It is a filter, not a security boundary:** it lowers the odds that planted text reaches your agent, and it can be fooled. Dropping a chunk also drops useful content alongside the planted text; the eval lost 3–4 complete goals out of 66 per planted-line run. See [the eval](evals/skimmer/0.1.103/results.md#injection-check) for how often it catches planted lines and how often it flags clean text.

## Prompts

Each chunk or item gets one yes/no Jev question, rendered from a template. The defaults are [`prompts/skim_document/DEFAULT.md`](prompts/skim_document/DEFAULT.md) and [`prompts/filter_results/DEFAULT.md`](prompts/filter_results/DEFAULT.md).

```bash
skimmer prompts list
skimmer prompts show skim_document
skimmer prompts init skim_document strict   # copies the default to ~/.config/skimmer/prompts/skim_document/strict.md
skimmer skim ./src -g "..." --prompt strict
skimmer skim ./src -g "..." --prompt ./my-prompt.md
```

A prompt is optional YAML frontmatter (`name`, `description`, `model`, and `criteria` with `true` and `false` text saying what yes and no mean) followed by a Go `text/template` body. The body can use `{{.Goal}}`, `{{.Text}}`, `{{.ID}}`, `{{.Index}}`, and, for skim, `{{.Source}}` and `{{.Lines}}`. A user prompt named `DEFAULT.md` replaces the built-in default. The built-in defaults send each chunk once in the request state and point the question at it (`{{.ID}}`) rather than pasting the text into the question, which measured 14.5% fewer input tokens with no measurable difference in recall (see the [corrections](evals/variants/README.md)).

## Best practices

These come from hands-on runs (below) and from [the evals](evals/skimmer/0.1.103/results.md).

| | without skimmer | with skimmer | answer kept? |
|---|---|---|---|
| Code search: "where do we retry or back off on 429s, and how long do we wait?" over 24 files | 179k chars | 65k at defaults; 17 chunks with `-t 0.8 -n 2` | yes, both ways |
| A Wikipedia article: Bloom filter false-positive formula and best number of hashes | 71k chars | 13k (82% less) | yes |
| The Go spec: closing nil or closed channels | 225k chars | 16k (93% less) | yes |
| 40 GitHub search results: "a production-quality Go Bloom filter library" | 40 items | 20 items | yes; close calls near p 0.5 were uneven |
| A goal the page can't answer | 71k chars | nothing kept | correctly empty |

**Write the goal as the question you need answered.** Name the specifics
("how long do we wait between attempts?", "the formula for the false-positive
rate"), not a topic ("retries", "bloom filters"). A topic matches every chunk
that mentions it. If the answer has several parts, put them all in one goal.

**Code search: narrow with `rg`, then skim the files.** `rg` is free; let it
pick the files, and let skimmer read them.

```bash
rg -l -i "retry|backoff|429" --type go . | skimmer skim -g "where do we retry on 429s, and how long do we wait?"
rg -l "TODO" -g '!*_test.go' . | skimmer skim -g "TODOs about auth or security"   # leave tests out
```

A broad question over many files keeps a lot at the defaults (43 chunks in
the run above). Tighten it with `-n 2` (at most two chunks per file, best
first), and add `-t 0.8` when you need to find *where* something is rather
than every place it is. Kept chunks are cited as `path:start-end`: open the file
there for more context.

**Web pages and long documents: skim the URL or file directly.** The defaults
work; expect 80–95% less to read. Skimmer fetches HTML, PDFs, and Reddit
threads itself.

**Lists and search results: `filter`, showing Jev only what matters.** Use
`--fields title,description` to hide noise, `--scores` to see each item's
probability, and `-n N` to keep the best N. Items scoring near 0.5 are judgment
calls; if precision matters, look at `--scores` and pick a cut yourself.

**Thresholds.** Keep the default 0.5. Raising it to 0.7 lost 8 of 42
held-out goals in the evals, mostly ones that need several passages. If a
goal needs *every* instance of something ("list every dataset"), try `-t 0.3`.

**When nothing comes back,** the answer is probably not there: goals the
material couldn't answer kept nothing. Rephrase once, or lower `-t`, before
reading raw. The reverse doesn't hold: when the material is on the right
subject but lacks the answer, skimmer kept something about 1 time in 5, so
check that what comes back actually answers the question.

**Untrusted pages:** add `--injection-check`. It costs about 25% more Jev
tokens, and it can drop a real answer that sits next to a planted line.

## For agents

Paste this into your `CLAUDE.md` / `AGENTS.md`:

```markdown
## Reading large material: use skimmer
Before reading a long web page, a big file, many grep hits, or a long list of results,
run it through `skimmer` with a specific goal, and read only what comes back:
- `skimmer skim <url|file|dir>... -g "<what you need>"`
- `rg -l "<pattern>" . | skimmer skim -g "<what you need>" -n 2`  (reads the matching files)
- `<command emitting JSON/lines> | skimmer filter -g "<what you need>" --fields title,description`
Write the goal as the specific question you need answered, not a topic.
Kept file chunks are cited as path:start-end; open the file there if you need more context.
If skimmer reports nothing relevant, rephrase the goal or lower `-t` before reading raw.
`skimmer --help` has a short guide to each kind of search.
```

## Evals

The skim filter is measured, not vibes: [`evals/`](evals/README.md) holds a
12-document public corpus (with attributions), three goal sets (find-the-fact,
multi-region, and on-topic goals the document doesn't answer), a frozen
dev/holdout split by document, a [pre-registration](evals/PREREG.md), and a
live harness that runs the real pipeline against a pinned model and archives
every chunk's probability. Latest results: [`evals/skimmer/0.1.103/`](evals/skimmer/0.1.103/results.md).
On held-out documents with `jev-1.14.0` and the defaults, counting goals with
95% Wilson intervals:

- find-the-fact goals keep their answer in 21 of 21 (0.85–1.00);
- all positive goals together are complete in 36 of 42 (0.72–0.93); every
  miss is on a multi-passage goal whose label a second model family
  (`gpt-6-astra`, which re-checked every label) disputes, and on the 30
  goals it agrees with, skimmer completes 30 (0.89–1.00);
- on-topic goals the document doesn't answer keep something in 3 of 12
  (0.09–0.53); far-off-topic goals in 0 of 6;
- a BM25 ranker keeping as many chunks per goal completes 19 of 42.

Threshold 0.5 is the measured default (0.7 loses 8 of 42 held-out goals), and
2000-character chunks stay (bigger chunks cost fewer Jev tokens but hand you
more text). The corpus is English only. Nothing in `go test` makes a live call.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The release process is in [AGENTS.md](AGENTS.md#release-strategy), and changes are listed in [CHANGELOG.md](CHANGELOG.md).

## License

MIT
