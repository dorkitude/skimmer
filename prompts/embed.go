// Package prompts embeds the default Jev prompt templates shipped with skimmer.
//
// Each kind of job has its own directory (skim_document/, filter_results/,
// injection_check/), and DEFAULT.md in each is used unless --prompt names
// another. A prompt file is YAML frontmatter followed by a Go text/template
// body that becomes each question's instructions. See internal/prompts for
// the template fields.
package prompts

import "embed"

// FS holds every prompt template, by kind.
//
//go:embed skim_document/*.md filter_results/*.md injection_check/*.md
var FS embed.FS
