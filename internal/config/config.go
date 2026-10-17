// Package config resolves skimmer's configuration.
//
// Precedence (highest first):
//  1. command-line flags (bound by the CLI layer)
//  2. environment variables (SKIMMER_API_KEY, SKIMMER_BASE_URL, ...)
//  3. a .env file in the current directory (never overrides the environment)
//  4. ~/.config/skimmer/config.yaml, written by `skimmer auth`
//  5. built-in defaults
//
// The API key also falls back to JEV_API_KEY and TYPESAFE_API_KEY.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"

	"github.com/dorkitude/skimmer/internal/jev"
)

// Defaults.
const (
	DefaultBaseURL     = "https://api.typesafe.ai"
	DefaultModel       = jev.DefaultModel
	DefaultThreshold   = 0.5
	DefaultChunkChars  = 2000
	DefaultOverlap     = 0.10
	DefaultMaxChars    = 400000
	DefaultConcurrency = 8
	EnvPrefix          = "SKIMMER"
	FileName           = "config.yaml"
)

// apiKeyEnvs are checked in order for the API key.
var apiKeyEnvs = []string{"SKIMMER_API_KEY", "JEV_API_KEY", "TYPESAFE_API_KEY"}

// Config is the resolved configuration.
type Config struct {
	// Dir is the skimmer config directory; File is config.yaml inside it.
	Dir  string
	File string

	APIKey string
	// KeySource says where APIKey came from: an env var name, ".env",
	// "config", or "" when unset.
	KeySource string

	BaseURL    string
	Model      string
	Threshold  float64
	ChunkChars int
	// Overlap is the fraction of each chunk repeated from the one before.
	Overlap     float64
	MaxChars    int
	Concurrency int
}

// Dir returns the config directory: $SKIMMER_CONFIG_DIR, else
// $XDG_CONFIG_HOME/skimmer, else ~/.config/skimmer.
func Dir() (string, error) {
	if d := os.Getenv("SKIMMER_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "skimmer"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "skimmer"), nil
}

// New returns a Viper instance with skimmer's defaults and env bindings.
func New() *viper.Viper {
	v := viper.New()
	v.SetDefault("base_url", DefaultBaseURL)
	v.SetDefault("model", DefaultModel)
	v.SetDefault("threshold", DefaultThreshold)
	v.SetDefault("chunk_chars", DefaultChunkChars)
	v.SetDefault("overlap", DefaultOverlap)
	v.SetDefault("max_chars", DefaultMaxChars)
	v.SetDefault("concurrency", DefaultConcurrency)
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	return v
}

// Load resolves configuration. v may carry bound flags; nil uses New().
func Load(v *viper.Viper) (*Config, error) {
	if v == nil {
		v = New()
	}
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	dotenv, err := loadDotEnv(".env")
	if err != nil {
		return nil, err
	}

	file := filepath.Join(dir, FileName)
	v.SetConfigFile(file)
	if err := v.ReadInConfig(); err != nil && !errors.Is(err, os.ErrNotExist) {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
	}

	cfg := &Config{
		Dir:         dir,
		File:        file,
		BaseURL:     strings.TrimRight(strings.TrimSpace(v.GetString("base_url")), "/"),
		Model:       strings.TrimSpace(v.GetString("model")),
		Threshold:   v.GetFloat64("threshold"),
		ChunkChars:  v.GetInt("chunk_chars"),
		Overlap:     v.GetFloat64("overlap"),
		MaxChars:    v.GetInt("max_chars"),
		Concurrency: v.GetInt("concurrency"),
	}
	cfg.APIKey, cfg.KeySource = resolveKey(v, dotenv)

	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Threshold < 0 || cfg.Threshold > 1 {
		return nil, fmt.Errorf("threshold must be between 0 and 1, got %g", cfg.Threshold)
	}
	if cfg.ChunkChars < 200 {
		return nil, fmt.Errorf("chunk_chars must be at least 200, got %d", cfg.ChunkChars)
	}
	if cfg.Overlap < 0 || cfg.Overlap > 0.5 {
		return nil, fmt.Errorf("overlap must be between 0 and 0.5, got %g", cfg.Overlap)
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultConcurrency
	}
	return cfg, nil
}

// resolveKey picks the API key: environment, then .env, then config.yaml.
// A --api-key flag, when bound to "api_key", wins over all of them.
func resolveKey(v *viper.Viper, dotenv map[string]string) (string, string) {
	if f := v.Get("api_key_flag"); f != nil {
		if s, _ := f.(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), "--api-key"
		}
	}
	for _, name := range apiKeyEnvs {
		if val := strings.TrimSpace(os.Getenv(name)); val != "" {
			return val, name
		}
	}
	for _, name := range apiKeyEnvs {
		if val := strings.TrimSpace(dotenv[name]); val != "" {
			return val, ".env (" + name + ")"
		}
	}
	if val := strings.TrimSpace(v.GetString("api_key")); val != "" {
		return val, "config"
	}
	return "", ""
}

// loadDotEnv reads KEY=VALUE lines from path. Every SKIMMER_* variable it
// defines is exported into the process environment unless already set, so
// viper sees it; the full map is returned for the API-key fallbacks. A
// missing file is not an error.
func loadDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		} else if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		out[k] = val
		if strings.HasPrefix(k, EnvPrefix+"_") && k != "SKIMMER_API_KEY" {
			if _, set := os.LookupEnv(k); !set {
				os.Setenv(k, val)
			}
		}
	}
	return out, sc.Err()
}

// Save merges values into config.yaml, creating the directory (0700) and
// file (0600) as needed. An empty string value deletes that key.
func Save(values map[string]any) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	file := filepath.Join(dir, FileName)
	current := map[string]any{}
	if b, err := os.ReadFile(file); err == nil {
		if err := yaml.Unmarshal(b, &current); err != nil {
			return "", fmt.Errorf("parse %s: %w", file, err)
		}
		if current == nil {
			current = map[string]any{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", file, err)
	}
	for k, val := range values {
		if s, ok := val.(string); ok && s == "" {
			delete(current, k)
			continue
		}
		current[k] = val
	}
	b, err := yaml.Marshal(current)
	if err != nil {
		return "", err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return "", fmt.Errorf("write %s: %w", file, err)
	}
	return file, nil
}

// KeyRequired reports whether talking to BaseURL needs an API key. A
// non-default base URL may be a proxy that injects credentials itself.
func (c *Config) KeyRequired() bool { return c.BaseURL == DefaultBaseURL }

// Mask shows only the edges of a secret.
func Mask(s string) string {
	if len(s) <= 8 {
		return strings.Repeat("•", len(s))
	}
	return s[:4] + strings.Repeat("•", 12)
}
