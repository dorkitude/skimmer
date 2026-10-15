package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinDefaultsRender(t *testing.T) {
	for _, kind := range Kinds {
		p, err := Load(kind, "", "")
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		out, err := p.Render(Data{Goal: "find the GOAL", ID: "item_3", Text: "the TEXT", Source: "a.go", Lines: "1-9"})
		if err != nil {
			t.Fatalf("%s render: %v", kind, err)
		}
		// Goal and text live in the request state; the question itself only
		// points at the item (state-pointer design), so only the id must
		// appear in the rendered question.
		for _, want := range []string{"item_3"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: rendered prompt lacks %q", kind, want)
			}
		}
	}
}

func TestUserPromptShadowsAndPathsWork(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, SkimDocument), 0o755)
	os.WriteFile(filepath.Join(dir, SkimDocument, "terse.md"), []byte("Is {{.ID}} about {{.Goal}}?"), 0o644)

	p, err := Load(SkimDocument, "terse", dir)
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := p.Render(Data{ID: "item_0", Goal: "cats"}); out != "Is item_0 about cats?" {
		t.Errorf("got %q", out)
	}
	p, err = Load(FilterResults, filepath.Join(dir, SkimDocument, "terse.md"), "")
	if err != nil || p.Name != "terse" {
		t.Fatalf("path load: %+v %v", p, err)
	}
	if _, err := Load(SkimDocument, "nope", dir); err == nil {
		t.Error("want error for unknown prompt")
	}

	names := map[string]string{}
	for _, e := range List(dir) {
		names[e.Kind+"/"+e.Name] = e.Origin
	}
	if names["skim_document/terse"] == "" || names["filter_results/DEFAULT"] != "built-in" {
		t.Errorf("List = %v", names)
	}
}

func TestParseRejectsScoreAndBadTemplates(t *testing.T) {
	if _, err := Parse("x.md", []byte("---\ntype: score\n---\nbody")); err == nil {
		t.Error("score prompts should be rejected")
	}
	if _, err := Parse("x.md", []byte("{{.Nope")); err == nil {
		t.Error("bad template should be rejected")
	}
}
