// Package adapters defines a small internal driver seam; no vendor SDK types
// cross into task state. This is not the external extension protocol.
package adapters

import (
	"context"
	"ucli.local/ucli/internal/core"
)

type Request struct {
	Attempt    core.Attempt
	Executable string
	StateHome  string
	// Binding selects an explicitly linked native account/configuration home.
	// It carries identity metadata only; native runtimes retain their credentials.
	// Drivers recheck the executable and current account before dispatch.
	Binding *NativeBinding
	// OnContext persists allocated or native-confirmed session provenance before
	// dispatch. Provenance never establishes provider turn acceptance. A failure
	// must stop dispatch. Nil supports standalone protocol experiments.
	OnContext func(core.NativeContext) error
}

// NativeBinding is a local connection descriptor, not an admission Boolean.
// Identity is an opaque account fingerprint, never a token or login address.
type NativeBinding struct {
	ID, Runtime, Executable, SHA256, Home, AuthMode, Identity, Version string
	// Separate homes must use provider-supported scoped credential storage.
	// Existing homes reference the user's native session without claiming isolation.
	HomeMode string
}
type Outcome struct {
	// State is complete, failed, interrupted or unknown. Unknown retains ownership
	// fences. A killed local process alone cannot settle remote model effects.
	State         string
	Message       string
	NativeSession string
	// NativeModel is an observed model identifier, when the runtime confirms it.
	// An alias in the accepted route remains the requested native selection.
	NativeModel string
}
type Driver interface {
	// Returned errors and Outcome.Message must be safe for durable user-facing
	// receipts; redact native diagnostics rather than propagating private text.
	Execute(context.Context, Request, func(string) error) (Outcome, error)
}
