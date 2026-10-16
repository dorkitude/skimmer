package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/items"
	"github.com/dorkitude/skimmer/internal/jev"
	"github.com/dorkitude/skimmer/internal/prompts"
	"github.com/dorkitude/skimmer/internal/skim"
)

var filterOpts struct {
	goal      string
	prompt    string
	input     string
	format    string
	key       string
	fields    []string
	itemChars int
	max       int
	scores    bool
	quiet     bool
	injection bool
}

var filterCmd = &cobra.Command{
	Use:   "filter [file] --goal GOAL",
	Short: "Keep only the items of a list that help with a goal",
	Long: `Filter an arbitrary list down to the items Jev believes help with the goal.

The list is read from the file argument, or stdin. It may be:
  - a JSON array (of strings, objects, anything), or an object holding one
    array (pick among several with --key)
  - JSON Lines, one value per line
  - plain lines of text (search hits, file names, log lines, ...)

The format is detected unless --format says otherwise, and kept items are
written back in the same format, in their original order, untouched. Use
--fields to show Jev only some fields of each object (e.g. title,snippet).`,
	Example: `  curl -s "$SEARCH_API?q=bloom+filters" | skimmer filter --key results --fields title,snippet -g "tutorials with code"
  gh issue list --json number,title,body | skimmer filter -g "bugs that could lose user data"
  ls ~/notes | skimmer filter -g "anything about the Q3 offsite"
  skimmer filter results.jsonl -g "papers from 2025 about sparse autoencoders" --scores`,
	Args: cobra.MaximumNArgs(1),
	RunE: runFilter,
}

func init() {
	f := filterCmd.Flags()
	f.StringVarP(&filterOpts.goal, "goal", "g", "", "what you are trying to accomplish (required)")
	f.StringVarP(&filterOpts.prompt, "prompt", "p", "", "prompt name or .md path (default filter_results/DEFAULT)")
	f.Float64P("threshold", "t", config.DefaultThreshold, "keep items with P(relevant) ≥ this")
	f.StringVar(&filterOpts.format, "format", "auto", "input format: auto, json, jsonl, or lines")
	f.StringVar(&filterOpts.key, "key", "", "for a JSON object input, the field holding the array")
	f.StringSliceVar(&filterOpts.fields, "fields", nil, "only show Jev these fields of each object")
	f.IntVar(&filterOpts.itemChars, "item-chars", 4000, "truncate each item to this many characters for Jev")
	f.IntVarP(&filterOpts.max, "max", "n", 0, "keep at most N items, best first (0 = no cap)")
	f.BoolVar(&filterOpts.scores, "scores", false, "include each kept item's index and P(relevant)")
	f.BoolVarP(&filterOpts.quiet, "quiet", "q", false, "no summary on stderr")
	f.BoolVar(&filterOpts.injection, "injection-check", false, "also ask whether each item tries to steer the reader, and drop the ones that do (a filter, not a security boundary)")
	_ = filterCmd.MarkFlagRequired("goal")
	rootCmd.AddCommand(filterCmd)
}

func runFilter(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(filterOpts.goal) == "" {
		return errors.New("--goal is required: say what you are trying to find or do")
	}
	format, err := items.ParseFormat(filterOpts.format)
	if err != nil {
		return err
	}
	cfg, err := loadConfigFor(cmd, map[string]string{"threshold": "threshold"})
	if err != nil {
		return err
	}

	var data []byte
	if len(args) == 1 && args[0] != "-" {
		data, err = os.ReadFile(args[0])
	} else {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("nothing to filter: pass a file or pipe a list on stdin")
		}
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		return err
	}
	list, err := items.Parse(data, format, filterOpts.key)
	if err != nil {
		return err
	}

	prompt, err := prompts.Load(prompts.FilterResults, filterOpts.prompt, promptDir(cfg))
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

	texts := make([]string, len(list.Raw))
	for i := range list.Raw {
		texts[i] = list.Text(i, filterOpts.fields, filterOpts.itemChars)
	}
	itemData := func(i int) prompts.Data {
		return prompts.Data{Goal: filterOpts.goal, ID: jev.ItemKey(i), Index: i, Text: texts[i]}
	}
	ask := prompt.Ask("", itemData)
	j := jev.Judgement{Goal: filterOpts.goal, Texts: texts, Instructions: ask.Instructions, Criteria: ask.Criteria}
	if filterOpts.injection {
		check, err := prompts.Load(prompts.InjectionCheck, "", promptDir(cfg))
		if err != nil {
			return err
		}
		j.Extra = append(j.Extra, check.Ask("injection", itemData))
	}
	v, err := client.JudgeAll(cmd.Context(), j)
	ps, usage := v.P, v.Usage
	var partial *jev.PartialError
	if err != nil && !errors.As(err, &partial) {
		return err
	}

	var keep, flagged []int
	unjudged := 0
	for i, p := range ps {
		switch {
		case p == nil:
			unjudged++
			keep = append(keep, i) // never silently drop what Jev didn't see
		case *p < cfg.Threshold:
		case filterOpts.injection && v.Extra["injection"][i] != nil && *v.Extra["injection"][i] >= skim.InjectionThreshold:
			flagged = append(flagged, i)
		default:
			keep = append(keep, i)
		}
	}
	if filterOpts.max > 0 && len(keep) > filterOpts.max {
		sort.SliceStable(keep, func(a, b int) bool { return score(ps[keep[a]]) > score(ps[keep[b]]) })
		keep = keep[:filterOpts.max]
		sort.Ints(keep)
	}

	out, err := list.Encode(keep, ps, filterOpts.scores)
	if err != nil {
		return err
	}
	if _, err := cmd.OutOrStdout().Write(out); err != nil {
		return err
	}
	if !filterOpts.quiet {
		msg := fmt.Sprintf("skimmer: kept %d of %d items (%s)", len(keep), len(list.Raw), list.Format)
		if unjudged > 0 {
			msg += fmt.Sprintf(", %d unjudged kept: %v", unjudged, partial)
		}
		if len(flagged) > 0 {
			msg += fmt.Sprintf(", %d dropped by --injection-check (indices %v)", len(flagged), flagged)
		}
		msg += usageNote(usage)
		fmt.Fprintln(os.Stderr, msg)
	}
	return nil
}

func score(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}
