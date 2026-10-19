// Package workflow validates a small workflow document and computes dependency
// readiness. It performs no execution, persistence, routing or authorization.
package workflow

import "ucli.local/ucli/internal/core"

const (
	SchemaVersion    = 1
	MaxSteps         = 256
	MaxEdges         = 2048
	MaxDocumentBytes = 2 * 1024 * 1024
	MaxPromptBytes   = core.MaxInputBytes
)

type Spec struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Steps   []Step `json:"steps"`
}

type Step struct {
	ID        string         `json:"id"`
	Prompt    string         `json:"prompt"`
	Selection core.Selection `json:"selection"`
	DependsOn []string       `json:"depends_on,omitempty"`
}

type State string

const (
	Pending     State = "pending"
	Running     State = "running"
	Complete    State = "complete"
	Failed      State = "failed"
	Interrupted State = "interrupted"
	Unknown     State = "unknown"
)

type Reason struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Dependency string `json:"dependency,omitempty"`
}

type BlockedStep struct {
	ID      string   `json:"id"`
	State   State    `json:"state"`
	Reasons []Reason `json:"reasons"`
}

type Plan struct {
	Ready    []string      `json:"ready"`
	Blocked  []BlockedStep `json:"blocked"`
	Running  []string      `json:"running"`
	Complete []string      `json:"complete"`
}

func problem(code, message string) error {
	e := core.Problem(code, message, 2)
	e.Acceptance = "not-sent"
	e.SideEffects = "none"
	return e
}
