// Package core defines UCLI's internal, provider-neutral execution vocabulary.
// These types are experimental and are not a public extension protocol.
package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type Selection struct {
	Model      string `json:"model"`
	Account    string `json:"account"`
	Effort     string `json:"effort"`
	Permission string `json:"permission"`
}

func Defaults() Selection { return Selection{"auto", "auto", "auto", "read-only"} }
func (s Selection) WithDefaults() Selection {
	d := Defaults()
	if s.Model != "" {
		d.Model = s.Model
	}
	if s.Account != "" {
		d.Account = s.Account
	}
	if s.Effort != "" {
		d.Effort = s.Effort
	}
	if s.Permission != "" {
		d.Permission = s.Permission
	}
	return d
}

type Workspace struct {
	ID   string `json:"id"`
	Root string `json:"root"`
	Dir  string `json:"directory"`
}
type Route struct {
	Runtime    string   `json:"runtime"`
	Provider   string   `json:"provider"`
	Model      string   `json:"model"`
	Account    string   `json:"account"`
	Effort     string   `json:"effort"`
	Permission string   `json:"permission"`
	Mode       string   `json:"mode"`
	Reasons    []string `json:"reasons"`
}
type Model struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Runtime   string   `json:"runtime"`
	Efforts   []string `json:"efforts"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
}
type Account struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Runtime  string `json:"runtime"`
	AuthMode string `json:"auth_mode"`
	Status   string `json:"status"`
}
type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Revision  int       `json:"revision"`
	Workspace Workspace `json:"workspace"`
	Draft     string    `json:"draft,omitempty"`
	Selection Selection `json:"selection"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Attempt struct {
	ID            string         `json:"id"`
	TaskID        string         `json:"task_id"`
	CommandID     string         `json:"command_id"`
	Revision      int            `json:"revision"`
	Workspace     Workspace      `json:"workspace"`
	Input         string         `json:"input"`
	Requested     Selection      `json:"requested"`
	Effective     Route          `json:"effective"`
	NativeContext *NativeContext `json:"native_context,omitempty"`
	State         string         `json:"state"`
	// StopCause survives replay so a confirmed deadline remains distinct from
	// user interruption. It does not settle an unknown external outcome.
	StopCause string `json:"stop_cause,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	// Failure preserves the terminal error category across command replay. Its
	// sanitized message remains in Error; it does not settle unknown work.
	Failure    *FailureClass `json:"failure,omitempty"`
	Warnings   []string      `json:"warnings,omitempty"`
	Owner      Owner         `json:"owner"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at,omitempty"`
}

// FailureClass is durable terminal metadata, unlike Error whose Exit is omitted
// from machine output. It is valid only for a confirmed failed attempt.
type FailureClass struct {
	Code        string `json:"code"`
	Origin      string `json:"origin"`
	Acceptance  string `json:"acceptance"`
	SideEffects string `json:"side_effects"`
	Exit        int    `json:"exit"`
}

// NativeContext records a native session's namespace and durability. It is
// provenance, not permission to resume a session or proof of a remote outcome.
type NativeContext struct {
	Runtime    string `json:"runtime"`
	Provider   string `json:"provider"`
	Account    string `json:"account"`
	SessionID  string `json:"session_id"`
	Durability string `json:"durability"`
	// Allocated is a client-chosen reference; confirmed means the native runtime
	// acknowledged it. Neither establishes that a provider turn was accepted.
	Provenance string `json:"provenance,omitempty"`
}
type Owner struct {
	ID        string    `json:"instance_id"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}
type Event struct {
	Version   int       `json:"version"`
	TaskID    string    `json:"task_id"`
	AttemptID string    `json:"attempt_id"`
	Sequence  int64     `json:"sequence"`
	At        time.Time `json:"at"`
	Type      string    `json:"type"`
	Text      string    `json:"text,omitempty"`
	State     string    `json:"state,omitempty"`
}
type RunCommand struct {
	ID               string
	TaskID           string
	ExpectedRevision int
	Input            string
	Requested        Selection
	Effective        Route
	Workspace        Workspace
	Owner            Owner
}

// IntentHash excludes generated ownership and the resolver's effective route.
// Replaying the same command may inspect its receipt, never dispatch it again.
func (c RunCommand) IntentHash() string {
	b, _ := json.Marshal(struct {
		TaskID    string
		Revision  int
		Input     string
		Selection Selection
		Workspace Workspace
	}{c.TaskID, c.ExpectedRevision, c.Input, c.Requested, c.Workspace})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func NewID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("secure ID generation: %w", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// Error codes are stable within the experimental CLI; messages contain no secrets.
type Error struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	TaskID      string `json:"task_id,omitempty"`
	Retry       string `json:"retry"`
	Origin      string `json:"origin"`
	Acceptance  string `json:"acceptance"`
	SideEffects string `json:"side_effects"`
	Exit        int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func Problem(code, message string, exit int) *Error {
	retry := "manual"
	if exit == 7 {
		retry = "reconcile-first"
	}
	return &Error{Code: code, Message: message, Exit: exit, Retry: retry, Origin: "ucli", Acceptance: "unknown", SideEffects: "unknown"}
}
