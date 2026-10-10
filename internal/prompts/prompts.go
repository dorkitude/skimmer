// Package prompts parses and renders skimmer's prompt files: a YAML
// frontmatter block delimited by "---" lines, followed by a Go text/template
// body that becomes each Jev question's instructions.
package prompts

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"

	embedded "github.com/dorkitude/skimmer/prompts"
)

// Kinds of prompt, each a directory under prompts/.
const (
	SkimDocument  = "skim_document"
	FilterResults = "filter_results"

	// Default is the prompt used when none is named.
	Default = "DEFAULT"
)

// Kinds lists every prompt kind.
var Kinds = []string{SkimDocument, FilterResults}

// Prompt is a parsed prompt file.
type Prompt struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"` // only "noul" is supported
	Model       string `yaml:"model"`

	// Body is the raw template text after the frontmatter.
	Body string `yaml:"-"`
	// Origin says where the prompt was loaded from.
	Origin string `yaml:"-"`

	tmpl *template.Template
}

// Data is the template context available to prompt bodies.
type Data struct {
	Goal   string // what the caller is trying to accomplish
	ID     string // the item's id in the request state, e.g. "item_3"
	Index  int    // zero-based position in the full list
	Text   string // the chunk or item text
	Source string // URL or file path the chunk came from (skim only)
	Lines  string // line range of the chunk, e.g. "40-58" (skim only)
}

// Render executes the prompt body with data, returning trimmed text.
func (p *Prompt) Render(data Data) (string, error) {
	var buf bytes.Buffer
	if err := p.tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt %q: %w", p.Name, err)
	}
	return strings.TrimSpace(buf.String()), nil
}

// Parse parses prompt content. name is used for errors and as a fallback
// when the frontmatter omits "name".
func Parse(name string, content []byte) (*Prompt, error) {
	front, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: %w", name, err)
	}
	p := &Prompt{}
	if err := yaml.Unmarshal(front, p); err != nil {
		return nil, fmt.Errorf("prompt %s: parse frontmatter: %w", name, err)
	}
	if p.Name == "" {
		p.Name = strings.TrimSuffix(filepath.Base(name), ".md")
	}
	if p.Type == "" {
		p.Type = "noul"
	}
	if p.Type != "noul" {
		return nil, fmt.Errorf("prompt %s: type %q is not supported (skimmer asks yes/no questions: use noul)", name, p.Type)
	}
	p.Body = strings.TrimSpace(string(body))
	if p.Body == "" {
		return nil, fmt.Errorf("prompt %s: body is empty", name)
	}
	tmpl, err := template.New(p.Name).Option("missingkey=error").Parse(p.Body)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: parse template: %w", name, err)
	}
	p.tmpl = tmpl
	return p, nil
}

// splitFrontmatter separates the YAML block from the body. A file with no
// frontmatter is all body, so a plain markdown file works as a prompt.
func splitFrontmatter(content []byte) (front, body []byte, err error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	text = strings.TrimLeft(text, "\uFEFF")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, []byte(text), nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return []byte(strings.Join(lines[1:i], "\n")), []byte(strings.Join(lines[i+1:], "\n")), nil
		}
	}
	return nil, nil, errors.New("unterminated YAML frontmatter (no closing ---)")
}

// Load resolves a prompt of the given kind. ref may be:
//   - "" or "DEFAULT": the built-in default (unless the user overrides it)
//   - a path to a .md file
//   - a name, looked up in userDir/<kind>/<name>.md, then the built-ins
//
// userDir may be "" to skip user prompts.
func Load(kind, ref, userDir string) (*Prompt, error) {
	if ref == "" {
		ref = Default
	}
	if looksLikePath(ref) {
		content, err := os.ReadFile(ref)
		if err != nil {
			return nil, fmt.Errorf("load prompt: %w", err)
		}
		return withOrigin(Parse(ref, content))(ref)
	}
	name := strings.TrimSuffix(ref, ".md")
	if userDir != "" {
		path := filepath.Join(userDir, kind, name+".md")
		if content, err := os.ReadFile(path); err == nil {
			return withOrigin(Parse(path, content))(path)
		}
	}
	file := kind + "/" + name + ".md"
	content, err := fs.ReadFile(embedded.FS, file)
	if err != nil {
		return nil, fmt.Errorf("no %s prompt named %q (see `skimmer prompts list`)", kind, name)
	}
	return withOrigin(Parse(file, content))("built-in")
}

func withOrigin(p *Prompt, err error) func(string) (*Prompt, error) {
	return func(origin string) (*Prompt, error) {
		if err != nil {
			return nil, err
		}
		p.Origin = origin
		return p, nil
	}
}

func looksLikePath(ref string) bool {
	return strings.ContainsRune(ref, os.PathSeparator) || strings.HasSuffix(ref, ".md") && fileExists(ref)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// Entry describes one available prompt.
type Entry struct {
	Kind   string
	Name   string
	Origin string // "built-in" or the user file path
}

// List returns every prompt of every kind: built-ins plus any in userDir.
// A user prompt with a built-in's name shadows it.
func List(userDir string) []Entry {
	var out []Entry
	for _, kind := range Kinds {
		seen := map[string]Entry{}
		if entries, err := fs.ReadDir(embedded.FS, kind); err == nil {
			for _, e := range entries {
				if n, ok := strings.CutSuffix(e.Name(), ".md"); ok {
					seen[n] = Entry{kind, n, "built-in"}
				}
			}
		}
		if userDir != "" {
			dir := filepath.Join(userDir, kind)
			if entries, err := os.ReadDir(dir); err == nil {
				for _, e := range entries {
					if n, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() {
						seen[n] = Entry{kind, n, filepath.Join(dir, e.Name())}
					}
				}
			}
		}
		names := make([]string, 0, len(seen))
		for n := range seen {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, seen[n])
		}
	}
	return out
}

// Raw returns the source text of a resolved prompt, for `skimmer prompts show`.
func Raw(kind, ref, userDir string) (string, string, error) {
	p, err := Load(kind, ref, userDir)
	if err != nil {
		return "", "", err
	}
	if p.Origin == "built-in" {
		name := strings.TrimSuffix(ref, ".md")
		if name == "" {
			name = Default
		}
		b, err := fs.ReadFile(embedded.FS, kind+"/"+name+".md")
		return string(b), p.Origin, err
	}
	b, err := os.ReadFile(p.Origin)
	return string(b), p.Origin, err
}
