package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/prompts"
)

var promptsCmd = &cobra.Command{
	Use:   "prompts",
	Short: "List, show, or customize the Jev prompts",
	Long: `skimmer asks Jev one yes/no question per chunk (skim) or item (filter),
rendered from a prompt template. The built-in defaults are
skim_document/DEFAULT and filter_results/DEFAULT.

Add your own as ~/.config/skimmer/prompts/<kind>/<NAME>.md and pick them with
--prompt NAME (a file named DEFAULT.md replaces the built-in default), or pass
--prompt path/to/file.md. A prompt is optional YAML frontmatter (name,
description, model) and a Go text/template body with these fields:

  {{.Goal}}    what the caller is trying to accomplish
  {{.Text}}    the chunk or item text
  {{.ID}}      its id in the request state, e.g. item_3
  {{.Index}}   its zero-based position
  {{.Source}}  the URL or file path (skim only)
  {{.Lines}}   the chunk's line range, e.g. 40-58 (skim only)`,
}

var promptsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available prompts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := userPromptDir()
		if err != nil {
			return err
		}
		for _, e := range prompts.List(dir) {
			fmt.Printf("%-15s %-12s %s\n", e.Kind, e.Name, e.Origin)
		}
		return nil
	},
}

var promptsShowCmd = &cobra.Command{
	Use:   "show <skim_document|filter_results> [NAME]",
	Short: "Print a prompt's source",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := userPromptDir()
		if err != nil {
			return err
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		src, origin, err := prompts.Raw(args[0], name, dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "# %s\n", origin)
		fmt.Print(src)
		return nil
	},
}

var promptsInitCmd = &cobra.Command{
	Use:   "init <skim_document|filter_results> NAME",
	Short: "Copy the default prompt to ~/.config/skimmer/prompts to customize it",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := userPromptDir()
		if err != nil {
			return err
		}
		src, _, err := prompts.Raw(args[0], prompts.Default, "")
		if err != nil {
			return err
		}
		path := filepath.Join(dir, args[0], args[1]+".md")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return err
		}
		fmt.Printf("📝 Wrote %s. Edit it, then use --prompt %s\n", path, args[1])
		return nil
	},
}

func userPromptDir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "prompts"), nil
}

func init() {
	promptsCmd.AddCommand(promptsListCmd, promptsShowCmd, promptsInitCmd)
	rootCmd.AddCommand(promptsCmd)
}
