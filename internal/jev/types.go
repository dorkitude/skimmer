// Package jev is a client for TypeSafe's Jev System One API.
package jev

import (
	"fmt"
	"strings"
)

// QuestionType enumerates the supported System One question types.
type QuestionType string

const (
	TypeScore QuestionType = "score"
	TypeNoul  QuestionType = "noul"
)

// Question is one typed question inside a SystemOneRequest.
type Question struct {
	Type         QuestionType `json:"type"`
	Instructions string       `json:"instructions"`
	// Criteria is a Noul's *NoulCriteria or a Score's []string of levels.
	Criteria any `json:"criteria,omitempty"`
}

// NoulCriteria says what a yes and a no mean, for a yes/no question whose
// boundary is subtle.
type NoulCriteria struct {
	True  string `json:"true" yaml:"true"`
	False string `json:"false" yaml:"false"`
}

// NoulQuestion builds a yes/no question. criteria may be nil.
func NoulQuestion(instructions string, criteria *NoulCriteria) Question {
	q := Question{Type: TypeNoul, Instructions: instructions}
	if criteria != nil {
		q.Criteria = criteria
	}
	return q
}

// SystemOneRequest is the POST /v1/systemone body.
type SystemOneRequest struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Answer is the raw per-question answer.
type Answer struct {
	Type QuestionType `json:"type"`
	// Noul is the probability that the answer is "yes".
	Noul *float64 `json:"noul,omitempty"`
	// Score is set for score questions.
	Score *float64 `json:"score,omitempty"`
}

// Usage reports token consumption for a request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// Model is the versioned model ID that answered (the response's model
	// field). Requests answered by different models list them all, comma
	// separated.
	Model string `json:"-"`
}

// Add accumulates another usage record into u.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.Model = joinModels(u.Model, o.Model)
}

func joinModels(a, b string) string {
	if b == "" {
		return a
	}
	for _, m := range strings.Split(a, ",") {
		if m == b {
			return a
		}
	}
	if a == "" {
		return b
	}
	return a + "," + b
}

// SystemOneResponse is the POST /v1/systemone response.
type SystemOneResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// AsNoul returns P(yes) for a noul answer, or errors if the type mismatches.
func (a Answer) AsNoul() (float64, error) {
	if a.Type != TypeNoul || a.Noul == nil {
		return 0, fmt.Errorf("expected noul answer, got type %q", a.Type)
	}
	return *a.Noul, nil
}
