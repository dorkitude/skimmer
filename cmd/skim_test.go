package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPathsFromList(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	os.WriteFile(a, []byte("x"), 0o644)
	os.WriteFile(b, []byte("y"), 0o644)

	grep := a + ":12:func main() {\n" + a + ":40:return\n" + b + ":3:package b\n"
	if got := pathsFromList(grep); !reflect.DeepEqual(got, []string{a, b}) {
		t.Errorf("grep -n output: %v", got)
	}
	if got := pathsFromList(a + "\n\n" + b + "\n"); !reflect.DeepEqual(got, []string{a, b}) {
		t.Errorf("plain list: %v", got)
	}
	if got := pathsFromList(a + "\nthis is prose, not a path\n"); got != nil {
		t.Errorf("prose must not be read as paths: %v", got)
	}
}

func TestLooksLikeDomain(t *testing.T) {
	for in, want := range map[string]bool{
		"example.com": true, "go.dev/doc": true, "notes.md": false, "./x": false, "readme": false,
	} {
		if got := looksLikeDomain(in); got != want {
			t.Errorf("looksLikeDomain(%q) = %v", in, got)
		}
	}
}
