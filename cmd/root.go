// Package cmd is skimmer's command-line interface.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/jev"
)

// Version is set at build time with -ldflags "-X github.com/dorkitude/skimmer/cmd.Version=..."
// (GoReleaser and the Homebrew formula do). `go install ...@vX.Y.Z` builds
// fall back to the module version; anything else reports "dev".
var Version = ""

func version() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

// v carries flag bindings into config.Load.
var v = config.New()

// errQuiet is returned after a command has already reported its failure.
var errQuiet = errors.New("")

var rootCmd = &cobra.Command{
	Use:   "skimmer",
	Short: "Skim pages, files, and lists with Jev: keep only what helps with your goal",
	Long: `skimmer saves an agent (or you) from reading junk.

Give it a goal and some material: web pages, files on disk, a pile of grep
hits, or a list of results. It splits the material into chunks, asks Jev
(https://typesafe.ai) whether each chunk helps with the goal, and returns only
the ones that do. Fewer tokens read means lower cost, faster turns, and a
less distracted model.

  skimmer skim https://go.dev/doc/effective_go -g "how should I name interfaces?"
  grep -rl "retry" ./internal | skimmer skim -g "where do we back off on 429s?"
  cat results.json | skimmer filter -g "papers from 2025 about sparse autoencoders"

Best practices (details and measurements in the README):
  Goal      Ask the specific question you need answered, not a topic.
            Put every part of a multi-part question in one goal.
  Code      Narrow with rg, then skim the files it lists. For broad
            questions over many files, add -n 2 (best two chunks per
            file); add -t 0.8 when you need where something is, not every
            place it is. Exclude tests with rg -g '!*_test.go'.
              rg -l -i "retry|429" --type go . | skimmer skim -g "..." -n 2
  Pages     Skim URLs and long files directly; the defaults keep 5-20%.
  Lists     skimmer filter --fields title,description (hide noise),
            --scores (see each item's p), -n N (keep the best N).
  Threshold Keep 0.5. Raising it drops multi-part answers; for "list
            every X" goals, try -t 0.3.
  Empty     Nothing kept usually means the answer isn't there: rephrase
            once or lower -t. When something IS kept, check it answers
            the question; on-topic material can lack the answer.
  Untrusted Add --injection-check (about 25% more Jev tokens).

Run ` + "`skimmer auth`" + ` once to store your Jev key.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.Version = version()
	jev.UserAgent = "skimmer/" + rootCmd.Version + " (+https://github.com/dorkitude/skimmer)"
	pf := rootCmd.PersistentFlags()
	pf.String("api-key", "", "Jev API key (default: $SKIMMER_API_KEY, .env, or `skimmer auth`)")
	pf.String("base-url", "", "Jev API base URL (default "+config.DefaultBaseURL+")")
	pf.String("model", "", "Jev model (default "+config.DefaultModel+")")
	pf.Int("concurrency", 0, fmt.Sprintf("max parallel Jev requests (default %d)", config.DefaultConcurrency))
	_ = v.BindPFlag("api_key_flag", pf.Lookup("api-key"))
	_ = v.BindPFlag("base_url", pf.Lookup("base-url"))
	_ = v.BindPFlag("model", pf.Lookup("model"))
	_ = v.BindPFlag("concurrency", pf.Lookup("concurrency"))
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if err != errQuiet {
			fmt.Fprintln(os.Stderr, "skimmer:", err)
		}
		return 1
	}
	return 0
}

// newClient builds a Jev client, failing early with a helpful message when
// no key is configured for the default endpoint.
func newClient(cfg *config.Config) (*jev.Client, error) {
	if cfg.APIKey == "" && cfg.KeyRequired() {
		return nil, errors.New("no Jev API key: run `skimmer auth`, set SKIMMER_API_KEY, or put it in .env (get one at https://typesafe.ai)")
	}
	c := jev.NewClient(cfg.APIKey)
	c.BaseURL = cfg.BaseURL
	c.Model = cfg.Model
	c.SetConcurrency(cfg.Concurrency)
	return c, nil
}

// loadConfigFor binds a command's local flags to config keys, then loads.
// Binding happens at run time because skim and filter share flag names
// (--threshold), and one viper key can only be bound to one flag.
func loadConfigFor(cmd *cobra.Command, flagToKey map[string]string) (*config.Config, error) {
	for flag, key := range flagToKey {
		if f := cmd.Flags().Lookup(flag); f != nil {
			_ = v.BindPFlag(key, f)
		}
	}
	return config.Load(v)
}
