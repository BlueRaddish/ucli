// Package routing selects a route from caller-owned, already scoped evidence.
// It performs no discovery, provider calls, credential access or reservation.
package routing

import (
	"time"

	"ucli.local/ucli/internal/core"
)

type Preference string

// DefaultCatalogAge bounds cached automatic model selection.
const DefaultCatalogAge = 24 * time.Hour

const (
	Balanced Preference = "balanced"
	Faster   Preference = "faster"
	Conserve Preference = "conserve"
	Quality  Preference = "quality"
)

// Capability is evidence admitted for this candidate's exact connection,
// runtime/configuration and workspace. Conditional support needs resolved guards.
type Capability struct {
	Support       string `json:"support"` // native, mapped, conditional, unsupported, unverified
	ConditionsMet bool   `json:"conditions_met,omitempty"`
}

type Effort struct {
	Level  string `json:"level"` // low, medium, high, max; ascending semantic order
	Native string `json:"native"`
}

type Money struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

// PoolKey identifies an actual shared allowance, not a model alias. Accounts
// sharing an organization allowance must reference the same key.
type PoolKey struct {
	Scope  string `json:"scope"`
	Bucket string `json:"bucket"`
}

type PoolUse struct {
	Key      PoolKey  `json:"key"`
	Unit     string   `json:"unit"`
	Estimate *float64 `json:"estimate,omitempty"`
}

type PoolSnapshot struct {
	Key           PoolKey   `json:"key"`
	Unit          string    `json:"unit"` // exact unit identity, e.g. tokens, requests, fraction, money:USD
	Remaining     *float64  `json:"remaining,omitempty"`
	Reserve       float64   `json:"reserve"`
	LocalReserved float64   `json:"local_reserved"`
	Inflight      int       `json:"inflight"` // includes uncertain external work; never TTL-expired locally
	ObservedAt    time.Time `json:"observed_at"`
	ResetAt       time.Time `json:"reset_at,omitempty"`
	Blocked       bool      `json:"blocked"`   // explicit exhaustion/429; no automatic reset
	Unmetered     bool      `json:"unmetered"` // explicit admitted absence of this quota
}

type Candidate struct {
	ID        string         `json:"id"`
	Route     core.Route     `json:"route"` // identity only; resolver fills effort, permission and reasons
	Workspace core.Workspace `json:"workspace"`

	Admitted          bool   `json:"admitted"`
	Available         bool   `json:"available"`
	WorkspaceAllowed  bool   `json:"workspace_allowed"`
	AuthMode          string `json:"auth_mode"` // none is explicit; otherwise binding + authentication required
	Authenticated     bool   `json:"authenticated"`
	BindingVerified   bool   `json:"binding_verified"`
	PrimaryModelFixed bool   `json:"primary_model_fixed"`

	Advertised        bool      `json:"advertised"`
	CatalogObservedAt time.Time `json:"catalog_observed_at"`
	Configured        bool      `json:"configured"`  // intentional connection configuration for an unlisted model
	Entitlement       string    `json:"entitlement"` // verified, advertised, unknown, denied, not-required

	Capabilities  map[string]Capability            `json:"capabilities"`
	Permissions   map[string]map[string]Capability `json:"permissions"` // dimension -> guarantee -> evidence
	Efforts       []Effort                         `json:"efforts"`
	DefaultEffort string                           `json:"default_effort"`
	QualityClass  int                              `json:"quality_class"` // 0 unknown; caller's comparable evaluation scale
	ContextLimit  int64                            `json:"context_limit"` // 0 unknown
	Latency       time.Duration                    `json:"latency"`       // 0 unknown; caller's comparable completion estimate

	Billing        string    `json:"billing"`                    // included, unmetered, metered, unknown
	CostUpperBound *Money    `json:"cost_upper_bound,omitempty"` // enforceable bound, not an optimistic estimate
	Pools          []PoolUse `json:"pools"`                      // all applicable windows, including unknown account/shared pools
}

type Request struct {
	Requested            core.Selection    `json:"requested"`
	Workspace            core.Workspace    `json:"workspace"`        // already resolved; no path discovery happens here
	AllowedAccounts      []string          `json:"allowed_accounts"` // mandatory explicit allowlist, also for no-auth connections
	Candidates           []Candidate       `json:"candidates"`
	Pools                []PoolSnapshot    `json:"pools"`
	Now                  time.Time         `json:"now"`             // required; deterministic and caller-controlled
	MaxCatalogAge        time.Duration     `json:"max_catalog_age"` // default 24h
	MaxQuotaAge          time.Duration     `json:"max_quota_age"`   // default 2m
	RequiredCapabilities []string          `json:"required_capabilities"`
	RequiredPermissions  map[string]string `json:"required_permissions"` // independent shell/network/external/approval policy
	MinQuality           int               `json:"min_quality"`
	ContextRequired      int64             `json:"context_required"`
	Preference           Preference        `json:"preference"`
	ContinuityID         string            `json:"continuity_id,omitempty"` // soft preference only, never a live-turn switch
	AllowFirstUse        bool              `json:"allow_first_use"`
	BoundedAttempt       bool              `json:"bounded_attempt"` // caller enforces task/time/output limits
	RequireKnownCapacity bool              `json:"require_known_capacity"`
	AllowMetered         bool              `json:"allow_metered"` // explicit spend authority, never inferred from auto
	MaxCost              *Money            `json:"max_cost,omitempty"`
}

type Reason struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Pool    *PoolKey `json:"pool,omitempty"`
}

type Exclusion struct {
	CandidateID string   `json:"candidate_id"`
	Reasons     []Reason `json:"reasons"`
}

type RankedCandidate struct {
	CandidateID   string        `json:"candidate_id"`
	Continuity    bool          `json:"continuity"`
	FirstUse      bool          `json:"first_use"`
	CapacityKnown bool          `json:"capacity_known"`
	Pressure      *float64      `json:"pressure,omitempty"` // largest estimated share of usable headroom; never summed
	QualityClass  int           `json:"quality_class"`
	Latency       time.Duration `json:"latency"`
	Cost          *Money        `json:"cost,omitempty"`
}

type Decision struct {
	Requested       core.Selection    `json:"requested"`
	CandidateID     string            `json:"candidate_id,omitempty"`
	Effective       core.Route        `json:"effective"`
	NativeEffort    string            `json:"native_effort,omitempty"`
	Reasons         []Reason          `json:"reasons"`
	ExcludedReasons []Exclusion       `json:"excluded_reasons"`
	Ranking         []RankedCandidate `json:"ranking"`
}
