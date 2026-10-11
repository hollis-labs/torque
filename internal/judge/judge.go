// Package judge is Torque's side of structured judgments: provider-neutral
// questions (yes/no, pick one, rate on a scale) asked about a piece of state,
// answered with probabilities. The model behind it (JEV, through Tether's
// Decide operation) chooses among options Torque defines; it never writes
// text, and Torque owns the question sets, the thresholds and what happens
// with an answer. See the design draft in Tesseract workspace
// jev_decision_provider_research_and_design_2026_10_11.
package judge

import (
	"context"
	"time"
)

// QuestionType is one of the three judgment shapes.
type QuestionType string

const (
	Noul   QuestionType = "noul"   // yes/no: the probability a statement is true
	Choice QuestionType = "choice" // pick one of a fixed set
	Score  QuestionType = "score"  // place on ordered levels
)

// Option is one choice of a Choice question, with the description the model reads.
type Option struct {
	Key         string `yaml:"key" json:"key"`
	Description string `yaml:"description" json:"description"`
}

// Question is one judgment, in provider-neutral form. Options and Levels are
// ordered: option order matters to the model (it leans toward the first).
type Question struct {
	Key          string       `json:"key"`
	Type         QuestionType `json:"type"`
	Instructions string       `json:"instructions"`
	Options      []Option     `json:"options,omitempty"` // Choice
	Levels       []string     `json:"levels,omitempty"`  // Score, lowest first
	True         string       `json:"true,omitempty"`    // Noul: what yes means
	False        string       `json:"false,omitempty"`   // Noul: what no means
}

// Request asks a set of independent questions about one state. Questions are
// answered in parallel and never see each other's answers.
type Request struct {
	State     any        `json:"state"`
	Questions []Question `json:"questions"` // ordered; Key identifies the answer
	Model     string     `json:"model,omitempty"`
	Purpose   string     `json:"purpose,omitempty"`
}

// Answer is the model's answer to one question.
type Answer struct {
	Type          QuestionType       `json:"type"`
	Choice        string             `json:"choice,omitempty"`        // Choice: the most likely option key
	Noul          float64            `json:"noul,omitempty"`          // Noul: probability of yes
	Score         float64            `json:"score,omitempty"`         // Score: probability-weighted level index
	Probabilities map[string]float64 `json:"probabilities,omitempty"` // Choice: per option key; Score: per level index
	Confidence    float64            `json:"confidence"`              // see Confidence
}

// Usage is what a call cost in tokens.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response carries the answers by question key.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
	Latency time.Duration     `json:"latency"`
}

// Decider answers a Request. Tether's Decide operation implements it for
// JEV; Baseline and Scripted implement it for evaluation and tests.
type Decider interface {
	Name() string
	Decide(ctx context.Context, req Request) (Response, error)
}
