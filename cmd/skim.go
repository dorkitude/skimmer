package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/fetch"
	"github.com/dorkitude/skimmer/internal/jev"
	"github.com/dorkitude/skimmer/internal/prompts"
	"github.com/dorkitude/skimmer/internal/skim"
)

var skimOpts struct {
	goal       string
	prompt     string
	maxKept    int
	maxFiles   int
	asJSON     bool
	raw        bool
	pathsStdin bool
	textStdin  bool
	quiet      bool
}

var skimCmd = &cobra.Command{
	Use:     "skim [url | file | dir | -]... --goal GOAL",
	Aliases: []string{"read", "url", "files"},
	Short:   "Return only the chunks of pages or files that help with a goal",
	Long: `Skim web pages, files, or stdin and print only the chunks Jev believes help
with the goal.

Each argument is a URL (http/https), a file, a directory (walked for text
files, skipping .git, node_modules, and hidden dirs), or "-" for stdin text.
With no arguments, stdin is read: if every line names a file (as from
` + "`grep -l`" + `, ` + "`rg -n`" + `, or ` + "`find`" + `), those files are skimmed; otherwise stdin
itself is the document. --paths and --text force either reading.

File chunks are labeled path:start-end so you can jump straight to them.
A summary of what was saved goes to stderr (silence it with -q).`,
	Example: `  skimmer skim https://en.wikipedia.org/wiki/Bloom_filter -g "false positive rate formula"
  skimmer skim ./docs README.md -g "how do I configure retries?"
  rg -l "context.WithTimeout" | skimmer skim -g "which calls lack a timeout on the HTTP client?"
  rg -n "TODO" | skimmer skim -g "TODOs about auth or security"
  pbpaste | skimmer skim -g "action items assigned to Kyle"`,
	RunE: runSkim,
}

func init() {
	f := skimCmd.Flags()
	f.StringVarP(&skimOpts.goal, "goal", "g", "", "what you are trying to accomplish (required)")
	f.StringVarP(&skimOpts.prompt, "prompt", "p", "", "prompt name or .md path (default skim_document/DEFAULT)")
	f.Float64P("threshold", "t", config.DefaultThreshold, "keep chunks with P(relevant) ≥ this")
	f.Int("chunk-chars", config.DefaultChunkChars, "target chunk size in characters")
	f.Int("max-chars", config.DefaultMaxChars, "read at most this many characters per source")
	f.IntVarP(&skimOpts.maxKept, "max-chunks", "n", 0, "keep at most N chunks per source, best first (0 = no cap)")
	f.IntVar(&skimOpts.maxFiles, "max-files", 500, "cap on files read from directories and stdin path lists")
	f.BoolVar(&skimOpts.asJSON, "json", false, "print results as JSON")
	f.BoolVar(&skimOpts.raw, "raw", false, "print only the kept text, no headers")
	f.BoolVar(&skimOpts.pathsStdin, "paths", false, "treat stdin as a list of file paths")
	f.BoolVar(&skimOpts.textStdin, "text", false, "treat stdin as the document")
	f.BoolVarP(&skimOpts.quiet, "quiet", "q", false, "no summary on stderr")
	_ = skimCmd.MarkFlagRequired("goal")
	rootCmd.AddCommand(skimCmd)
}

func runSkim(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(skimOpts.goal) == "" {
		return errors.New("--goal is required: say what you are trying to find or do")
	}
	if skimOpts.pathsStdin && skimOpts.textStdin {
		return errors.New("--paths and --text are mutually exclusive")
	}
	cfg, err := loadConfigFor(cmd, map[string]string{
		"threshold": "threshold", "chunk-chars": "chunk_chars", "max-chars": "max_chars",
	})
	if err != nil {
		return err
	}
	sources, warnings, err := resolveSources(args)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "skimmer:", w)
	}
	if len(sources) == 0 {
		return errors.New("nothing to skim: pass URLs, files, or directories, or pipe text or paths on stdin")
	}
	prompt, err := prompts.Load(prompts.SkimDocument, skimOpts.prompt, promptDir(cfg))
	if err != nil {
		return err
	}
	client, err := newClient(cfg)
	if err != nil {
		return err
	}
	if prompt.Model != "" && !cmd.Flags().Changed("model") {
		client.Model = prompt.Model
	}

	s := &skim.Skimmer{
		Client:     client,
		Fetcher:    &fetch.Fetcher{MaxChars: cfg.MaxChars},
		Prompt:     prompt,
		Goal:       skimOpts.goal,
		Threshold:  cfg.Threshold,
		ChunkChars: cfg.ChunkChars,
		MaxChars:   cfg.MaxChars,
		MaxKept:    skimOpts.maxKept,
	}
	results, usage := s.Run(cmd.Context(), sources)

	out := cmd.OutOrStdout()
	switch {
	case skimOpts.asJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(struct {
			Goal    string        `json:"goal"`
			Results []skim.Result `json:"results"`
			Usage   jev.Usage     `json:"jev_usage"`
		}{skimOpts.goal, results, usage}); err != nil {
			return err
		}
	case skimOpts.raw:
		printRaw(out, results)
	default:
		printSkim(out, results)
	}
	if !skimOpts.quiet {
		printSkimSummary(results, usage)
	}
	for _, r := range results {
		if r.Error == "" {
			return nil
		}
	}
	return errQuiet // every source failed; the errors are already printed
}

// resolveSources turns arguments (or stdin) into sources.
func resolveSources(args []string) ([]skim.Source, []string, error) {
	var warnings []string
	if len(args) == 0 {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return nil, nil, errors.New("nothing to skim: pass URLs, files, or directories, or pipe text or paths on stdin")
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, nil, fmt.Errorf("read stdin: %w", err)
		}
		paths := pathsFromList(string(data))
		switch {
		case skimOpts.textStdin, !skimOpts.pathsStdin && paths == nil:
			return []skim.Source{{Kind: skim.KindStdin, Ref: "-", Text: string(data)}}, nil, nil
		case paths == nil:
			return nil, nil, errors.New("--paths: no line of stdin names an existing file")
		}
		args = paths
	}

	var out []skim.Source
	seen := map[string]bool{}
	files, capped := 0, false
	add := func(src skim.Source) {
		if seen[src.Ref] {
			return
		}
		seen[src.Ref] = true
		out = append(out, src)
	}
	for _, a := range args {
		switch {
		case a == "-":
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, nil, fmt.Errorf("read stdin: %w", err)
			}
			add(skim.Source{Kind: skim.KindStdin, Ref: "-", Text: string(data)})
		case strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://"):
			add(skim.Source{Kind: skim.KindURL, Ref: a})
		default:
			st, err := os.Stat(a)
			if err != nil {
				if looksLikeDomain(a) {
					add(skim.Source{Kind: skim.KindURL, Ref: "https://" + a})
					continue
				}
				warnings = append(warnings, fmt.Sprintf("skipping %s: %v", a, err))
				continue
			}
			remaining := skimOpts.maxFiles - files
			if remaining <= 0 {
				capped = true
				continue
			}
			if !st.IsDir() {
				add(skim.Source{Kind: skim.KindFile, Ref: a})
				files++
				continue
			}
			list, truncated, err := skim.ExpandDir(a, remaining)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("walking %s: %v", a, err))
			}
			capped = capped || truncated
			for _, p := range list {
				add(skim.Source{Kind: skim.KindFile, Ref: p})
				files++
			}
		}
	}
	if capped {
		warnings = append(warnings, fmt.Sprintf("stopped at %d files (--max-files)", skimOpts.maxFiles))
	}
	return out, warnings, nil
}

// pathsFromList reads stdin as a list of files, one per line, accepting
// grep/rg style "path:line:match" lines. It returns nil unless every
// non-empty line resolves to an existing file, so ordinary text is never
// mistaken for a path list. Paths are deduplicated in first-seen order.
func pathsFromList(data string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p := ""
		if isFile(line) {
			p = line
		} else if i := strings.IndexByte(line, ':'); i > 0 && isFile(line[:i]) {
			p = line[:i]
		}
		if p == "" {
			return nil
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// looksLikeDomain accepts "example.com/path" typed without a scheme.
func looksLikeDomain(a string) bool {
	host, _, _ := strings.Cut(a, "/")
	return strings.Contains(host, ".") && !strings.ContainsAny(host, " \\") &&
		!strings.HasPrefix(a, ".") && !strings.HasSuffix(host, ".md") && !strings.HasSuffix(host, ".txt")
}

func promptDir(cfg *config.Config) string {
	return cfg.Dir + string(os.PathSeparator) + "prompts"
}

// printSkim prints each source's kept chunks. Sources with nothing relevant
// are listed together at the end, so a wide grep doesn't spend a line (and
// an agent's tokens) on every miss.
func printSkim(w io.Writer, results []skim.Result) {
	var empty []string
	first := true
	section := func() {
		if !first {
			fmt.Fprintln(w)
		}
		first = false
	}
	for _, r := range results {
		switch {
		case r.Error != "":
			section()
			fmt.Fprintf(w, "## %s: error: %s\n", r.Source, r.Error)
			continue
		case len(r.Kept) == 0:
			empty = append(empty, label(r))
			continue
		}
		section()
		note := ""
		if r.Truncated {
			note = ", truncated"
		}
		if r.Unjudged > 0 {
			note += fmt.Sprintf(", %d unjudged kept", r.Unjudged)
		}
		fmt.Fprintf(w, "## %s: kept %d of %d chunks (%s of %s chars%s)\n",
			label(r), len(r.Kept), r.Chunks, human(r.KeptChars), human(r.Chars), note)
		for _, k := range r.Kept {
			fmt.Fprintf(w, "\n[%s · p=%s]\n%s\n", where(r, k), pstr(k.P), k.Text)
		}
	}
	if len(empty) > 0 {
		section()
		fmt.Fprintf(w, "## nothing relevant in %d source(s): %s\n", len(empty), strings.Join(empty, ", "))
	}
}

func printRaw(w io.Writer, results []skim.Result) {
	first := true
	for _, r := range results {
		for _, k := range r.Kept {
			if !first {
				fmt.Fprintln(w)
			}
			first = false
			fmt.Fprintln(w, k.Text)
		}
	}
}

func label(r skim.Result) string {
	if r.Kind == skim.KindStdin {
		return "stdin"
	}
	return r.Source
}

// where cites a kept chunk: path:lines for files and stdin, chunk index for
// pages (whose line numbers are of the extracted text, not the HTML).
func where(r skim.Result, k skim.Kept) string {
	switch r.Kind {
	case skim.KindFile:
		return r.Source + ":" + k.Lines()
	case skim.KindStdin:
		return "lines " + k.Lines()
	}
	return fmt.Sprintf("chunk %d", k.Index+1)
}

func pstr(p *float64) string {
	if p == nil {
		return "?"
	}
	return fmt.Sprintf("%.2f", *p)
}

func human(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func printSkimSummary(results []skim.Result, usage jev.Usage) {
	var chunks, kept, chars, keptChars, failed int
	for _, r := range results {
		if r.Error != "" {
			failed++
			continue
		}
		chunks += r.Chunks
		kept += len(r.Kept)
		chars += r.Chars
		keptChars += r.KeptChars
	}
	saved := 0.0
	if chars > 0 {
		saved = 100 * (1 - float64(keptChars)/float64(chars))
	}
	msg := fmt.Sprintf("skimmer: %d source(s), kept %d/%d chunks, %s → %s chars (%.0f%% saved)",
		len(results), kept, chunks, human(chars), human(keptChars), saved)
	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}
	if usage.InputTokens > 0 {
		msg += fmt.Sprintf(" · jev %s tokens", human(usage.InputTokens+usage.OutputTokens))
	}
	fmt.Fprintln(os.Stderr, msg)
}
