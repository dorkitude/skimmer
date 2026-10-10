package config

import (
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SKIMMER_CONFIG_DIR", filepath.Join(dir, "cfg"))
	for _, k := range append(apiKeyEnvs, "SKIMMER_BASE_URL", "SKIMMER_THRESHOLD") {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	wd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(wd) })
	return dir
}

func TestPrecedenceEnvOverDotEnvOverFile(t *testing.T) {
	dir := setup(t)
	if _, err := Save(map[string]any{"api_key": "from-file", "threshold": 0.7}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "from-file" || cfg.KeySource != "config" || cfg.Threshold != 0.7 {
		t.Errorf("file: %+v", cfg)
	}
	st, _ := os.Stat(cfg.File)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("config.yaml mode = %o", st.Mode().Perm())
	}

	os.WriteFile(filepath.Join(dir, ".env"), []byte("# c\nexport JEV_API_KEY=\"from-dotenv\"\nSKIMMER_THRESHOLD=0.3\n"), 0o600)
	cfg, _ = Load(nil)
	if cfg.APIKey != "from-dotenv" || cfg.Threshold != 0.3 {
		t.Errorf(".env: key=%q src=%q threshold=%v", cfg.APIKey, cfg.KeySource, cfg.Threshold)
	}

	t.Setenv("SKIMMER_API_KEY", "from-env")
	cfg, _ = Load(nil)
	if cfg.APIKey != "from-env" || cfg.KeySource != "SKIMMER_API_KEY" {
		t.Errorf("env: key=%q src=%q", cfg.APIKey, cfg.KeySource)
	}
}

func TestSaveEmptyDeletes(t *testing.T) {
	setup(t)
	Save(map[string]any{"api_key": "k", "base_url": "https://proxy"})
	Save(map[string]any{"api_key": ""})
	cfg, _ := Load(nil)
	if cfg.APIKey != "" || cfg.BaseURL != "https://proxy" || cfg.KeyRequired() {
		t.Errorf("got %+v", cfg)
	}
}

func TestMaskHidesSecret(t *testing.T) {
	if got := Mask("sk-abcdefghijklmnop"); got != "sk-a••••••••••••" {
		t.Errorf("Mask = %q", got)
	}
}
