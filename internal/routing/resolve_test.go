package routing

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

var testNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
var testPool = PoolKey{Scope: "organization:test", Bucket: "weekly"}

func n(v float64) *float64 { return &v }
func fixture() Request {
	w := core.Workspace{ID: "workspace", Root: "C:/project", Dir: "C:/project/src"}
	c := Candidate{
		ID: "route-a", Route: core.Route{Runtime: "runtime", Provider: "provider", Model: "model-a", Account: "account-a", Mode: "structured"}, Workspace: w,
		Admitted: true, Available: true, WorkspaceAllowed: true, AuthMode: "native", Authenticated: true, BindingVerified: true, PrimaryModelFixed: true,
		Advertised: true, CatalogObservedAt: testNow, Entitlement: "verified",
		Capabilities: map[string]Capability{"streaming": {Support: "native"}, "tools": {Support: "mapped"}},
		Permissions: map[string]map[string]Capability{
			"filesystem": {"read-only": {Support: "native"}, "workspace-write": {Support: "mapped"}},
			"network":    {"denied": {Support: "native"}},
		},
		Efforts: []Effort{{"low", "minimal"}, {"medium", "normal"}, {"high", "deep"}}, DefaultEffort: "medium",
		QualityClass: 2, ContextLimit: 10000, Latency: 10 * time.Second, Billing: "included",
		Pools: []PoolUse{{Key: testPool, Unit: "tokens", Estimate: n(10)}},
	}
	return Request{
		Requested: core.Defaults(), Workspace: w, AllowedAccounts: []string{"account-a"}, Candidates: []Candidate{c}, Now: testNow,
		Pools:                []PoolSnapshot{{Key: testPool, Unit: "tokens", Remaining: n(100), ObservedAt: testNow, ResetAt: testNow.Add(time.Hour)}},
		RequiredCapabilities: []string{"streaming"}, RequiredPermissions: map[string]string{"network": "denied"}, BoundedAttempt: true,
	}
}

func resolveOK(t *testing.T, r Request) Decision {
	t.Helper()
	d, err := Resolve(r)
	if err != nil {
		t.Fatalf("Resolve: %v; exclusions=%+v", err, d.ExcludedReasons)
	}
	return d
}
func excluded(t *testing.T, r Request, code string) Decision {
	t.Helper()
	d, err := Resolve(r)
	if err == nil {
		t.Fatalf("expected exclusion %s, got success %+v", code, d)
	}
	var p *core.Error
	if !errors.As(err, &p) {
		t.Fatalf("untyped error: %v", err)
	}
	for _, e := range d.ExcludedReasons {
		for _, why := range e.Reasons {
			if why.Code == code {
				return d
			}
		}
	}
	t.Fatalf("missing exclusion %s in %+v (error %v)", code, d.ExcludedReasons, err)
	return d
}

func TestIndependentPinsAndEffortMapping(t *testing.T) {
	r := fixture()
	b := r.Candidates[0]
	b.ID = "route-b"
	b.Route.Model = "model-b"
	b.Route.Account = "account-b"
	b.Latency = time.Second
	r.Candidates = append(r.Candidates, b)
	r.AllowedAccounts = append(r.AllowedAccounts, "account-b")
	r.Requested.Model = "model-a"
	d := resolveOK(t, r)
	if d.CandidateID != "route-a" || d.Effective.Effort != "medium" || d.NativeEffort != "normal" {
		t.Fatalf("pin/default mapping lost: %+v", d)
	}
	r.Requested.Model = "auto"
	r.Requested.Account = "account-a"
	r.Requested.Effort = "max"
	d = resolveOK(t, r)
	if d.CandidateID != "route-a" || d.Effective.Effort != "high" || d.NativeEffort != "deep" {
		t.Fatalf("account/max mapping lost: %+v", d)
	}
	r.Requested.Model = "model-b"
	excluded(t, r, "account_pin")
	r.Requested.Account = "auto"
	r.Requested.Effort = "low"
	if d = resolveOK(t, r); d.CandidateID != "route-b" || d.Effective.Effort != "low" {
		t.Fatalf("independent effort pin lost: %+v", d)
	}
}

func TestEffortNeverSilentlyDowngraded(t *testing.T) {
	r := fixture()
	r.Requested.Effort = "high"
	r.Candidates[0].Efforts = r.Candidates[0].Efforts[:2]
	excluded(t, r, "effort")
	r.Requested.Effort = "max"
	if d := resolveOK(t, r); d.Effective.Effort != "medium" {
		t.Fatalf("max must mean highest admitted effort: %+v", d)
	}
	r.Candidates[0].Efforts = nil
	r.Candidates[0].DefaultEffort = ""
	excluded(t, r, "effort")
	r.Requested.Effort = "auto"
	if d := resolveOK(t, r); d.Effective.Effort != "default" || d.NativeEffort != "" {
		t.Fatalf("effort-free runtime was emulated: %+v", d)
	}
}

func TestAccountScopeBindingAndPrimaryFallback(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*Request)
	}{
		{"outside scope", "account_scope", func(r *Request) { r.AllowedAccounts = []string{"other"}; r.Requested.Account = "account-a" }},
		{"ambient auth", "account_binding", func(r *Request) { r.Candidates[0].BindingVerified = false }},
		{"revoked", "account_binding", func(r *Request) { r.Candidates[0].Authenticated = false }},
		{"pinned fallback", "model_fallback", func(r *Request) { r.Requested.Model = "model-a"; r.Candidates[0].PrimaryModelFixed = false }},
		{"unadmitted", "unadmitted", func(r *Request) { r.Candidates[0].Admitted = false }},
		{"missing runtime", "unavailable", func(r *Request) { r.Candidates[0].Available = false }},
	} {
		t.Run(tc.name, func(t *testing.T) { r := fixture(); tc.change(&r); excluded(t, r, tc.code) })
	}
}

func TestNoAuthLocalRouteNeedsTrustNotFakeCredentials(t *testing.T) {
	r := fixture()
	c := &r.Candidates[0]
	c.AuthMode = "none"
	c.Authenticated = false
	c.BindingVerified = false
	c.Advertised = false
	c.Entitlement = "not-required"
	r.Pools[0] = PoolSnapshot{Key: testPool, Unit: "tokens", Unmetered: true}
	r.Requested.Account = "account-a"
	r.BoundedAttempt = false
	resolveOK(t, r)
	c.Admitted = false
	excluded(t, r, "unadmitted")
	c.Admitted = true
	c.AuthMode = "native"
	c.Authenticated = true
	c.BindingVerified = true
	excluded(t, r, "entitlement")
}

func TestWorkspaceEvidenceCannotMoveAcrossDirectories(t *testing.T) {
	for _, change := range []func(*Request){
		func(r *Request) { r.Workspace.ID = "other" },
		func(r *Request) { r.Workspace.Root = "C:/other" },
		func(r *Request) { r.Workspace.Dir = "C:/project/other" },
		func(r *Request) { r.Candidates[0].WorkspaceAllowed = false },
	} {
		r := fixture()
		change(&r)
		excluded(t, r, "workspace_scope")
	}
}

func TestCapabilitiesAndEveryPermissionDimension(t *testing.T) {
	for _, support := range []string{"unsupported", "unverified", "conditional"} {
		t.Run(support, func(t *testing.T) {
			r := fixture()
			r.Candidates[0].Capabilities["streaming"] = Capability{Support: support}
			excluded(t, r, "capability")
		})
	}
	r := fixture()
	r.Candidates[0].Capabilities["streaming"] = Capability{Support: "conditional", ConditionsMet: true}
	resolveOK(t, r)
	r.RequiredCapabilities = append(r.RequiredCapabilities, "native.resume")
	excluded(t, r, "capability")
	r = fixture()
	delete(r.Candidates[0].Permissions["filesystem"], "read-only")
	excluded(t, r, "permission")
	r = fixture()
	r.Candidates[0].Permissions["network"]["denied"] = Capability{Support: "unverified"}
	excluded(t, r, "permission_dimension")
	r = fixture()
	r.RequiredPermissions["external-mutations"] = "denied"
	excluded(t, r, "permission_dimension")
	r = fixture()
	r.Requested.Permission = "unrestricted"
	excluded(t, r, "permission")
	r = fixture()
	r.MinQuality = 3
	excluded(t, r, "minimum_quality")
	r = fixture()
	r.ContextRequired = 10001
	excluded(t, r, "context")
}

func TestEntitlementIsSeparateFromCatalogAndQuota(t *testing.T) {
	r := fixture()
	r.Candidates[0].Entitlement = "denied"
	r.AllowFirstUse = true
	excluded(t, r, "entitlement_denied")
	r = fixture()
	r.Candidates[0].Entitlement = "advertised"
	excluded(t, r, "first_use")
	r.AllowFirstUse = true
	if d := resolveOK(t, r); !d.Ranking[0].FirstUse {
		t.Fatal("advertised is not verified")
	}
	r.BoundedAttempt = false
	excluded(t, r, "first_use")
	r.BoundedAttempt = true
	r.Pools[0].Inflight = 1
	excluded(t, r, "first_use_busy")
	r.Pools[0].Inflight = 0
	r.Candidates[0].CatalogObservedAt = testNow.Add(-25 * time.Hour)
	excluded(t, r, "catalog")
	r.Candidates[0].Entitlement = "unknown"
	r.Candidates[0].Advertised = false
	excluded(t, r, "catalog")
	r.Requested.Model = "model-a"
	resolveOK(t, r)
	r.Requested.Model = "auto"
	r.Candidates[0].Configured = true
	resolveOK(t, r)
	r = fixture()
	r.Pools[0].Remaining = nil
	if d := resolveOK(t, r); d.Ranking[0].FirstUse || d.Ranking[0].CapacityKnown {
		t.Fatal("unknown quota must not change entitlement")
	}
}

func TestUnknownQuotaOneInflightAndStrictPolicy(t *testing.T) {
	r := fixture()
	r.Pools[0].Remaining = nil
	d := resolveOK(t, r)
	if d.Ranking[0].CapacityKnown || d.Ranking[0].Pressure != nil {
		t.Fatal("unknown capacity misreported")
	}
	r.Pools[0].Inflight = 1
	excluded(t, r, "unknown_pool_busy")
	r.Pools[0].Inflight = 0
	r.RequireKnownCapacity = true
	excluded(t, r, "capacity_unknown")
	r.RequireKnownCapacity = false
	r.BoundedAttempt = false
	excluded(t, r, "capacity_unknown")
	r = fixture()
	r.Candidates[0].Pools[0].Estimate = nil
	resolveOK(t, r)
	r.Pools[0].Inflight = 1
	excluded(t, r, "unknown_pool_busy")
}

func TestSharedPoolsAndEveryApplicableWindow(t *testing.T) {
	r := fixture()
	r.Pools[0].Remaining = nil
	r.Pools[0].Inflight = 1
	b := r.Candidates[0]
	b.ID = "route-b"
	b.Route.Model = "other-alias"
	b.Route.Account = "account-b"
	r.Candidates = append(r.Candidates, b)
	r.AllowedAccounts = append(r.AllowedAccounts, "account-b")
	d := excluded(t, r, "unknown_pool_busy")
	if len(d.ExcludedReasons) != 2 {
		t.Fatal("model/account alias created fresh quota")
	}
	r = fixture()
	short := PoolKey{Scope: testPool.Scope, Bucket: "five-hour"}
	r.Candidates[0].Pools = append(r.Candidates[0].Pools, PoolUse{Key: short, Unit: "fraction", Estimate: n(.1)})
	r.Pools = append(r.Pools, PoolSnapshot{Key: short, Unit: "fraction", Remaining: n(0), ObservedAt: testNow})
	excluded(t, r, "capacity_exhausted")
	r.Pools[1].Remaining = n(.5)
	resolveOK(t, r)
	r.Pools = r.Pools[:1]
	excluded(t, r, "pool_missing")
}

func TestReservationAccountingAndUnitIsolation(t *testing.T) {
	r := fixture()
	p := &r.Pools[0]
	p.Remaining = n(100)
	p.Reserve = 10
	p.LocalReserved = 80
	resolveOK(t, r) // exactly ten left, estimate charged once
	p.LocalReserved = 81
	excluded(t, r, "capacity_insufficient")
	p.LocalReserved = 90
	excluded(t, r, "capacity_exhausted")
	r = fixture()
	r.Candidates[0].Pools[0].Unit = "fraction"
	excluded(t, r, "pool_unit")
	r = fixture()
	r.Pools[0].Blocked = true
	excluded(t, r, "capacity_exhausted")
}

func TestStaleResetAndClockShiftNeverRestoreCapacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PoolSnapshot)
	}{
		{"stale", func(p *PoolSnapshot) { p.ObservedAt = testNow.Add(-3 * time.Minute) }},
		{"future timestamp", func(p *PoolSnapshot) { p.ObservedAt = testNow.Add(time.Minute) }},
		{"passed reset", func(p *PoolSnapshot) { p.ObservedAt = testNow.Add(-time.Minute); p.ResetAt = testNow.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture()
			tc.change(&r.Pools[0])
			d := resolveOK(t, r)
			if d.Ranking[0].CapacityKnown {
				t.Fatal("stale observation trusted")
			}
			r.Pools[0].Inflight = 1
			excluded(t, r, "unknown_pool_busy")
		})
	}
	r := fixture()
	r.Pools[0].ObservedAt = testNow.Add(-time.Hour)
	r.Pools[0].ResetAt = testNow.Add(-time.Minute)
	r.Pools[0].Remaining = n(0)
	excluded(t, r, "capacity_exhausted")
	r.Pools[0].ObservedAt = testNow
	r.Pools[0].Remaining = n(100)
	if d := resolveOK(t, r); !d.Ranking[0].CapacityKnown {
		t.Fatal("fresh post-reset observation not used")
	}
}

func TestNoAutomaticPaidBillingOrCurrencyConversion(t *testing.T) {
	r := fixture()
	c := &r.Candidates[0]
	c.Billing = "metered"
	c.CostUpperBound = &Money{"USD", 1}
	excluded(t, r, "spend")
	r.AllowMetered = true
	excluded(t, r, "spend")
	r.MaxCost = &Money{"USD", .5}
	excluded(t, r, "spend")
	r.MaxCost = &Money{"EUR", 2}
	excluded(t, r, "spend")
	r.MaxCost = &Money{"USD", 2}
	resolveOK(t, r)
	c.CostUpperBound = nil
	excluded(t, r, "spend")
	c.Billing = "unknown"
	excluded(t, r, "billing_unknown")
}

func TestPreferenceRankingIsTransparentAndStable(t *testing.T) {
	r := fixture()
	r.Candidates[0].Latency = 20 * time.Second
	b := r.Candidates[0]
	b.ID = "route-b"
	b.Latency = time.Second
	b.QualityClass = 1
	b.Entitlement = "advertised"
	b.Pools = []PoolUse{{Key: testPool, Unit: "tokens", Estimate: n(5)}}
	c := r.Candidates[0]
	c.ID = "route-c"
	c.Latency = 30 * time.Second
	c.QualityClass = 3
	c.Pools = []PoolUse{{Key: testPool, Unit: "tokens", Estimate: n(1)}}
	r.Candidates = append(r.Candidates, b, c)
	r.AllowFirstUse = true
	for _, tc := range []struct {
		preference Preference
		want       string
	}{{Balanced, "route-a"}, {Faster, "route-b"}, {Quality, "route-c"}, {Conserve, "route-c"}} {
		r.Preference = tc.preference
		d := resolveOK(t, r)
		if d.CandidateID != tc.want || len(d.Ranking) != 3 || len(d.Reasons) < 2 {
			t.Fatalf("%s decision: %+v", tc.preference, d)
		}
	}
	r.ContinuityID = "route-a"
	r.Preference = Quality
	if d := resolveOK(t, r); d.CandidateID != "route-a" {
		t.Fatal("continuity lost")
	}
	r.Candidates[0].Entitlement = "denied"
	if d := resolveOK(t, r); d.CandidateID == "route-a" {
		t.Fatal("continuity bypassed eligibility")
	}
}

func TestConserveNormalizesWithinEachPoolWithoutSummingUnits(t *testing.T) {
	r := fixture()
	key := PoolKey{"org:test", "requests"}
	r.Pools = append(r.Pools, PoolSnapshot{Key: key, Unit: "requests", Remaining: n(4), ObservedAt: testNow})
	r.Candidates[0].Pools = append(r.Candidates[0].Pools, PoolUse{Key: key, Unit: "requests", Estimate: n(2)})
	r.Preference = Conserve
	d := resolveOK(t, r)
	if d.Ranking[0].Pressure == nil || *d.Ranking[0].Pressure != .5 {
		t.Fatalf("pressure should be max(10/100,2/4), never a unit sum: %+v", d.Ranking)
	}
}

func TestInputOrderAndConcurrentReadsCannotChangeDecision(t *testing.T) {
	r := fixture()
	b := r.Candidates[0]
	b.ID = "route-b"
	c := b
	c.ID = "route-c"
	r.Candidates = []Candidate{c, b, r.Candidates[0]}
	before, _ := json.Marshal(r)
	want := resolveOK(t, r)
	if want.CandidateID != "route-a" {
		t.Fatal("tie must use stable ID")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := Resolve(r)
			if err != nil || !reflect.DeepEqual(d, want) {
				t.Errorf("concurrent decision mismatch: %v", err)
			}
		}()
	}
	wg.Wait()
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("resolver mutated the caller snapshot")
	}
	sort.Slice(r.Candidates, func(i, j int) bool { return r.Candidates[i].ID < r.Candidates[j].ID })
	if d := resolveOK(t, r); !reflect.DeepEqual(d, want) {
		t.Fatal("candidate input order affected decision")
	}
	sortedInput, _ := json.Marshal(r)
	// Returned diagnostics/cost are owned by the decision, not aliases into input.
	want.Effective.Reasons[0] = "edited"
	want.Ranking[0].Cost.Amount = 123
	after, _ = json.Marshal(r)
	if string(after) != string(sortedInput) {
		t.Fatal("returned data aliases the input")
	}
}

func TestCapacityErrorClassAndModelFallbackDisclosure(t *testing.T) {
	r := fixture()
	r.Pools[0].Remaining = n(0)
	_, err := Resolve(r)
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Exit != 4 {
		t.Fatalf("expected capacity exit 4: %v", err)
	}
	r = fixture()
	r.AllowedAccounts = []string{"other"}
	_, err = Resolve(r)
	if !errors.As(err, &problem) || problem.Exit != 5 {
		t.Fatalf("expected policy exit 5: %v", err)
	}
	r = fixture()
	r.Candidates[0].PrimaryModelFixed = false
	d := resolveOK(t, r)
	for _, why := range d.Reasons {
		if why.Code == "primary_model_unfixed" {
			return
		}
	}
	t.Fatal("automatic native fallback uncertainty was hidden")
}

func TestMalformedSnapshotsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Request)
	}{
		{"no accounts", func(r *Request) { r.AllowedAccounts = nil }},
		{"no clock", func(r *Request) { r.Now = time.Time{} }},
		{"negative age", func(r *Request) { r.MaxQuotaAge = -time.Second }},
		{"future enum", func(r *Request) { r.Candidates[0].Entitlement = "new-schema-state" }},
		{"future capability", func(r *Request) { r.Candidates[0].Capabilities["streaming"] = Capability{Support: "probably"} }},
		{"duplicate route", func(r *Request) { r.Candidates = append(r.Candidates, r.Candidates[0]) }},
		{"duplicate pool", func(r *Request) { r.Pools = append(r.Pools, r.Pools[0]) }},
		{"duplicate usage", func(r *Request) { r.Candidates[0].Pools = append(r.Candidates[0].Pools, r.Candidates[0].Pools[0]) }},
		{"no pool inventory", func(r *Request) { r.Candidates[0].Pools = nil }},
		{"nan", func(r *Request) { r.Pools[0].Remaining = n(math.NaN()) }},
		{"infinite estimate", func(r *Request) { r.Candidates[0].Pools[0].Estimate = n(math.Inf(1)) }},
		{"negative inflight", func(r *Request) { r.Pools[0].Inflight = -1 }},
		{"ambiguous unmetered", func(r *Request) { r.Pools[0].Unmetered = true }},
		{"unordered efforts", func(r *Request) {
			r.Candidates[0].Efforts[0], r.Candidates[0].Efforts[1] = r.Candidates[0].Efforts[1], r.Candidates[0].Efforts[0]
		}},
		{"missing default effort", func(r *Request) { r.Candidates[0].DefaultEffort = "" }},
		{"contradictory envelope", func(r *Request) { r.RequiredPermissions["filesystem"] = "unrestricted" }},
		{"invalid preference", func(r *Request) { r.Preference = "expensive" }},
		{"zero workspace", func(r *Request) { r.Workspace = core.Workspace{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture()
			tc.change(&r)
			d, err := Resolve(r)
			var p *core.Error
			if !errors.As(err, &p) || p.Code != "invalid_routing_snapshot" || d.CandidateID != "" {
				t.Fatalf("malformed snapshot accepted: %+v %v", d, err)
			}
		})
	}
}
