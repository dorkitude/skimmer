// Package jev is a client for TypeSafe's Jev System One API.
package jev

import "fmt"

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
	Criteria     []string     `json:"criteria,omitempty"`
}

// NoulQuestion builds a yes/no question.
func NoulQuestion(instructions string) Question {
	return Question{Type: TypeNoul, Instructions: instructions}
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
}

// Add accumulates another usage record into u.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
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
