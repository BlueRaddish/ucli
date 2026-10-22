// Package connections stores references to native sign-ins, never credentials.
// Cached metadata is not account isolation, entitlement, or managed-AI admission.
package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

type Connection struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Provider   string    `json:"provider"`
	Executable string    `json:"executable"`
	SHA256     string    `json:"sha256"`
	NativeHome string    `json:"native_home"`
	HomeMode   string    `json:"home_mode"`
	AuthState  string    `json:"auth_state"`
	AuthMode   string    `json:"auth_mode"`
	Identity   string    `json:"identity,omitempty"`
	Version    string    `json:"version,omitempty"`
	PlanType   string    `json:"plan_type,omitempty"`
	Isolation  string    `json:"isolation"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	ManagedAI  bool      `json:"managed_ai"`
	Models     []Model   `json:"models,omitempty"`
	// Catalog evidence can come from a newer binary than the execution pin.
	// Unknown compatibility never erases the native account's durable identity.
	Compatibility string `json:"compatibility,omitempty"`
	CatalogSHA256 string `json:"catalog_sha256,omitempty"`
}
type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
	Default       bool     `json:"default"`
	ResolvedID    string   `json:"resolved_id,omitempty"`
	EffortState   string   `json:"effort_state,omitempty"`
	Unavailable   bool     `json:"unavailable,omitempty"`
}
type ConnectOptions struct{ Provider, Label, Executable, NativeHome, HomeMode string }
type Discovered struct {
	Provider   string `json:"provider"`
	Executable string `json:"executable,omitempty"`
	NativeHome string `json:"native_home,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Reason     string `json:"reason"`
	Available  bool   `json:"available"`
}

// LoginEvent may carry a short-lived URL/device code. Display only to the user;
// never persist it in task history, logs, metadata, or diagnostic receipts.
type LoginEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	URL     string `json:"url,omitempty"`
	Code    string `json:"code,omitempty"`
}

func (c Connection) Binding() adapters.NativeBinding {
	return adapters.NativeBinding{ID: c.ID, Runtime: c.Provider, Executable: c.Executable, SHA256: c.SHA256, Home: c.NativeHome, HomeMode: c.HomeMode, AuthMode: c.AuthMode, Identity: c.Identity, Version: c.Version}
}
func IdentityFingerprint(provider, nativeID string) string {
	if nativeID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(provider + "\x00" + nativeID))
	return hex.EncodeToString(sum[:])
}
func problem(code, message string) error {
	exit := 3
	switch code {
	case "connection_input", "connection_not_found":
		exit = 2
	case "connection_conflict", "connection_changed", "connection_busy":
		exit = 5
	case "connection_store", "connection_corrupt", "connection_cleanup":
		exit = 9
	}
	return core.Problem(code, message, exit)
}

type Service struct {
	home, directory string
	inspect         func(context.Context, Connection) (Connection, error)
	discover        func(context.Context, Connection) ([]Model, error)
}

// New and List never create directories, launch children or probe credentials.
func New(home string) (*Service, error) {
	physical, err := physicalPath(home)
	if err != nil {
		return nil, err
	}
	s := &Service{home: physical, directory: join(physical, "connections-v1")}
	s.inspect = s.inspectNative
	s.discover = discoverCatalog
	return s, nil
}
