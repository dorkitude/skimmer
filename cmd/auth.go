package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dorkitude/skimmer/internal/config"
	"github.com/dorkitude/skimmer/internal/jev"
	"github.com/dorkitude/skimmer/internal/secret"
)

var authOpts struct {
	stdin   bool
	noCheck bool
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Store your Jev API key in ~/.config/skimmer (one-time setup)",
	Long: `Prompt for your Jev API key, check it with one tiny request, and store it in
~/.config/skimmer/config.yaml (mode 0600). Get a key at https://typesafe.ai.

Pipe the key in with --stdin to keep it out of your shell history:
  pass show typesafe | skimmer auth --stdin

Behind a proxy that injects credentials itself (an exe.dev integration, say),
store its URL instead and skip the key:
  skimmer auth --base-url https://typesafe.int.exe.xyz

The environment (SKIMMER_API_KEY, JEV_API_KEY) and a .env file in the
current directory still override the stored key.`,
	Args: cobra.NoArgs,
	RunE: runAuth,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show where skimmer gets its key and endpoint, and check they work",
	Args:  cobra.NoArgs,
	RunE:  runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored key (and stored base URL) from ~/.config/skimmer",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, err := config.Save(map[string]any{"api_key": "", "base_url": ""})
		if err != nil {
			return err
		}
		fmt.Printf("🧹 Removed the stored key from %s\n", file)
		return nil
	},
}

func init() {
	authCmd.Flags().BoolVar(&authOpts.stdin, "stdin", false, "read the key from stdin")
	authCmd.Flags().BoolVar(&authOpts.noCheck, "no-check", false, "store without checking the key against Jev")
	authStatusCmd.Flags().BoolVar(&authOpts.noCheck, "no-check", false, "don't make a test request")
	authCmd.AddCommand(authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuth(cmd *cobra.Command, args []string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(mustString(cmd, "base-url")), "/")
	if baseURL == "" {
		baseURL = config.DefaultBaseURL
	}
	keyOptional := baseURL != config.DefaultBaseURL

	key := strings.TrimSpace(mustString(cmd, "api-key"))
	if key == "" {
		var err error
		switch {
		case authOpts.stdin:
			var b []byte
			b, err = readAllTrim(os.Stdin)
			key = string(b)
		case term.IsTerminal(int(os.Stdin.Fd())):
			label := "Jev API key (from https://typesafe.ai): "
			if keyOptional {
				label = "Jev API key (Enter to skip; " + baseURL + " may inject one): "
			}
			key, err = secret.PromptMasked(label)
		default:
			return errors.New("stdin is not a terminal: use --stdin to pipe the key in")
		}
		if err != nil {
			return err
		}
	}
	if key == "" && !keyOptional {
		return errors.New("no key given")
	}

	if !authOpts.noCheck {
		c := jev.NewClient(key)
		c.BaseURL = baseURL
		c.NoRetry = true
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		fmt.Print("🔎 Checking with Jev... ")
		if err := c.Validate(ctx); err != nil {
			fmt.Println("❌")
			return fmt.Errorf("%w (nothing saved; --no-check stores it anyway)", err)
		}
		fmt.Println("✅")
	}

	values := map[string]any{"api_key": key}
	if keyOptional {
		values["base_url"] = baseURL
	} else {
		values["base_url"] = "" // back to the default endpoint
	}
	file, err := config.Save(values)
	if err != nil {
		return err
	}
	fmt.Printf("🔐 Saved to %s (0600). Happy skimming!\n", file)
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(v)
	if err != nil {
		return err
	}
	fmt.Printf("config:   %s\n", cfg.File)
	fmt.Printf("endpoint: %s\n", cfg.BaseURL)
	switch {
	case cfg.APIKey != "":
		fmt.Printf("key:      %s (from %s)\n", config.Mask(cfg.APIKey), cfg.KeySource)
	case cfg.KeyRequired():
		fmt.Println("key:      none. Run `skimmer auth`, or set SKIMMER_API_KEY")
		return errQuiet
	default:
		fmt.Println("key:      none (the endpoint is expected to inject one)")
	}
	if authOpts.noCheck {
		return nil
	}
	client, err := newClient(cfg)
	if err != nil {
		return err
	}
	client.NoRetry = true
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err := client.Validate(ctx); err != nil {
		fmt.Printf("check:    ❌ %v\n", err)
		return errQuiet
	}
	fmt.Println("check:    ✅ Jev answered")
	return nil
}

func mustString(cmd *cobra.Command, flag string) string {
	s, _ := cmd.Flags().GetString(flag)
	return s
}

func readAllTrim(f *os.File) ([]byte, error) {
	b := make([]byte, 0, 256)
	buf := make([]byte, 256)
	for {
		n, err := f.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
	}
	return []byte(strings.TrimSpace(string(b))), nil
}
