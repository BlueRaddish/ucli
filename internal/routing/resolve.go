package routing

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/core"
)

type eligible struct {
	c       Candidate
	e       Effort
	rank    RankedCandidate
	reasons []Reason
}

// Resolve reads a snapshot at a new-task/checkpoint boundary. Success is a
// proposal, not acceptance: a caller must atomically recheck/reserve capacity
// before dispatch. The current experimental app has no such quota ledger.
func Resolve(req Request) (Decision, error) {
	d := Decision{Requested: req.Requested.WithDefaults(), Reasons: []Reason{}, ExcludedReasons: []Exclusion{}, Ranking: []RankedCandidate{}}
	req.Requested = d.Requested
	if err := validateRequest(req); err != nil {
		return d, err
	}
	if req.Preference == "" {
		req.Preference = Balanced
	}
	if req.MaxCatalogAge == 0 {
		req.MaxCatalogAge = 24 * time.Hour
	}
	if req.MaxQuotaAge == 0 {
		req.MaxQuotaAge = 2 * time.Minute
	}
	pools := make(map[PoolKey]PoolSnapshot, len(req.Pools))
	for _, p := range req.Pools {
		pools[p.Key] = p
	}
	allowed := make(map[string]bool, len(req.AllowedAccounts))
	for _, id := range req.AllowedAccounts {
		allowed[id] = true
	}
	var choices []eligible
	for _, c := range req.Candidates {
		v, excluded := evaluate(req, c, allowed, pools)
		if len(excluded) != 0 {
			d.ExcludedReasons = append(d.ExcludedReasons, Exclusion{c.ID, excluded})
		} else {
			choices = append(choices, v)
		}
	}
	sort.Slice(d.ExcludedReasons, func(i, j int) bool { return d.ExcludedReasons[i].CandidateID < d.ExcludedReasons[j].CandidateID })
	if len(choices) == 0 {
		exit := 5
		for _, excluded := range d.ExcludedReasons {
			capacityOnly := len(excluded.Reasons) > 0
			for _, why := range excluded.Reasons {
				capacityOnly = capacityOnly && oneOf(why.Code, "capacity_exhausted", "capacity_insufficient", "capacity_unknown", "unknown_pool_busy", "first_use_busy")
			}
			if capacityOnly {
				exit = 4
			}
		}
		return d, core.Problem("no_eligible_route", "No route satisfies the requested pins, account policy, evidence and capacity; inspect excluded_reasons.", exit)
	}
	sort.Slice(choices, func(i, j int) bool { return less(choices[i].rank, choices[j].rank, req.Preference) })
	for _, v := range choices {
		d.Ranking = append(d.Ranking, v.rank)
	}
	v := choices[0]
	d.CandidateID, d.NativeEffort = v.c.ID, v.e.Native
	d.Effective = v.c.Route
	d.Effective.Effort, d.Effective.Permission = v.e.Level, req.Requested.Permission
	d.Reasons = append(d.Reasons, v.reasons...)
	d.Reasons = append(d.Reasons, reason("ranking", rankingDescription(req.Preference)))
	d.Reasons = append(d.Reasons, reason("snapshot_only", "This snapshot selects a route; capacity is not reserved and must be rechecked before dispatch."))
	d.Effective.Reasons = make([]string, len(d.Reasons))
	for i, r := range d.Reasons {
		d.Effective.Reasons[i] = r.Message
	}
	return d, nil
}

func evaluate(r Request, c Candidate, allowed map[string]bool, pools map[PoolKey]PoolSnapshot) (eligible, []Reason) {
	v := eligible{c: c, rank: RankedCandidate{CandidateID: c.ID, Continuity: r.ContinuityID == c.ID, QualityClass: c.QualityClass, Latency: c.Latency, CapacityKnown: true}}
	var out []Reason
	exclude := func(code, msg string) { out = append(out, reason(code, msg)) }
	if !allowed[c.Route.Account] {
		exclude("account_scope", "The connection is outside the explicit allowed account set.")
	}
	if r.Requested.Account != "auto" && c.Route.Account != r.Requested.Account {
		exclude("account_pin", "The connection does not match the requested account.")
	}
	if r.Requested.Model != "auto" && c.Route.Model != r.Requested.Model {
		exclude("model_pin", "The exact model does not match the requested model.")
	}
	if r.Requested.Model != "auto" && !c.PrimaryModelFixed {
		exclude("model_fallback", "This runtime cannot guarantee the pinned primary model.")
	}
	if r.Requested.Model == "auto" && !c.PrimaryModelFixed {
		v.reasons = append(v.reasons, reason("primary_model_unfixed", "The runtime may change the auto-selected primary model; the adapter must record the actual model used."))
	}
	if !c.Admitted {
		exclude("unadmitted", "The route has no admission evidence for this connection and configuration.")
	}
	if !c.Available {
		exclude("unavailable", "The runtime is not ready.")
	}
	if c.Workspace != r.Workspace || !c.WorkspaceAllowed {
		exclude("workspace_scope", "Route evidence does not authorize this exact resolved workspace and directory.")
	}
	if c.AuthMode != "none" && (!c.Authenticated || !c.BindingVerified) {
		exclude("account_binding", "The authenticated runtime is not verifiably bound to this connection.")
	}
	if !supports(c.Permissions["filesystem"][r.Requested.Permission]) {
		exclude("permission", "The requested filesystem envelope is not enforced by this route.")
	}
	keys := make([]string, 0, len(r.RequiredPermissions))
	for dimension := range r.RequiredPermissions {
		keys = append(keys, dimension)
	}
	sort.Strings(keys)
	for _, dimension := range keys {
		if !supports(c.Permissions[dimension][r.RequiredPermissions[dimension]]) {
			exclude("permission_dimension", "The required "+dimension+" policy is not enforced by this route.")
		}
	}
	for _, name := range r.RequiredCapabilities {
		if !supports(c.Capabilities[name]) {
			exclude("capability", "Required capability "+name+" is unavailable or unverified.")
		}
	}
	if c.QualityClass < r.MinQuality {
		exclude("minimum_quality", "The evaluated quality class is below the task requirement.")
	}
	if r.ContextRequired > 0 && c.ContextLimit < r.ContextRequired {
		exclude("context", "The known context bound cannot fit the task.")
	}
	var ok bool
	v.e, ok = selectEffort(c, r.Requested.Effort)
	if !ok {
		exclude("effort", "The requested effort has no admitted native mapping; it will not be downgraded or prompt-emulated.")
	} else {
		v.reasons = append(v.reasons, reason("effort", fmt.Sprintf("Effort %s resolves to %s (native %q).", r.Requested.Effort, v.e.Level, v.e.Native)))
	}
	listed := c.Advertised && fresh(c.CatalogObservedAt, r.Now, r.MaxCatalogAge)
	switch c.Entitlement {
	case "denied":
		exclude("entitlement_denied", "Access was denied; account cycling is not a recovery for that denial.")
	case "not-required":
		if c.AuthMode != "none" {
			exclude("entitlement", "Only an admitted no-auth connection can declare entitlement not required.")
		}
	case "verified":
		if !listed && !c.Configured && r.Requested.Model == "auto" {
			exclude("catalog", "Auto selection needs a fresh advertised model or an intentionally configured connection.")
		}
	case "advertised", "unknown":
		if !r.AllowFirstUse || !r.BoundedAttempt {
			exclude("first_use", "Unverified entitlement requires an authorized bounded ordinary first attempt.")
		}
		if !listed && !c.Configured && r.Requested.Model == "auto" {
			exclude("catalog", "Unlisted or stale models are not explored automatically.")
		}
		if c.Entitlement == "advertised" && !listed {
			exclude("catalog", "Advertised first use requires a fresh catalog entry for this connection.")
		}
		v.rank.FirstUse = true
		v.reasons = append(v.reasons, reason("first_use", "Entitlement is unverified; this is one bounded ordinary task attempt, not an access probe."))
	}
	if c.Billing == "unknown" {
		exclude("billing_unknown", "Billing authority is unknown; automatic selection cannot enable paid use.")
	}
	if c.Billing == "metered" {
		if !r.AllowMetered || r.MaxCost == nil || c.CostUpperBound == nil || c.CostUpperBound.Currency != r.MaxCost.Currency || c.CostUpperBound.Amount > r.MaxCost.Amount {
			exclude("spend", "Metered use needs explicit authority and an enforceable upper bound within the same-currency budget.")
		} else {
			copy := *c.CostUpperBound
			v.rank.Cost = &copy
		}
	} else if c.Billing == "included" || c.Billing == "unmetered" {
		currency := ""
		if r.MaxCost != nil {
			currency = r.MaxCost.Currency
		}
		v.rank.Cost = &Money{Currency: currency, Amount: 0}
	}
	pressure := float64(0)
	v.rank.Pressure = &pressure
	for _, use := range c.Pools {
		p, exists := pools[use.Key]
		if !exists {
			out = append(out, poolReason("pool_missing", "An applicable allowance has no snapshot, including its local in-flight count.", use.Key))
			continue
		}
		if p.Unit != use.Unit {
			out = append(out, poolReason("pool_unit", "Usage estimates and allowance observations have different units.", use.Key))
			continue
		}
		if v.rank.FirstUse && p.Inflight > 0 {
			out = append(out, poolReason("first_use_busy", "Unverified entitlement permits one bounded first attempt; this pool already has in-flight work.", use.Key))
		}
		if p.Blocked || (p.Remaining != nil && *p.Remaining <= p.Reserve+p.LocalReserved) {
			out = append(out, poolReason("capacity_exhausted", "This allowance is exhausted or reserved; passing its reset time does not restore capacity.", use.Key))
			continue
		}
		if p.Unmetered {
			continue
		}
		known := p.Remaining != nil && use.Estimate != nil && fresh(p.ObservedAt, r.Now, r.MaxQuotaAge)
		if !p.ResetAt.IsZero() && !p.ResetAt.After(r.Now) && p.ObservedAt.Before(p.ResetAt) {
			known = false
		}
		if known {
			headroom := *p.Remaining - p.Reserve - p.LocalReserved
			if *use.Estimate > headroom {
				out = append(out, poolReason("capacity_insufficient", "Estimated usage exceeds observed headroom after the reserve and outstanding local claims.", use.Key))
			} else if v.rank.Pressure != nil {
				pressure = math.Max(pressure, *use.Estimate/headroom)
			}
		} else {
			v.rank.CapacityKnown, v.rank.Pressure = false, nil
			if r.RequireKnownCapacity || !r.BoundedAttempt {
				out = append(out, poolReason("capacity_unknown", "This policy requires known headroom, or a bounded unknown-capacity attempt.", use.Key))
			} else if p.Inflight > 0 {
				out = append(out, poolReason("unknown_pool_busy", "Unknown capacity permits at most one in-flight attempt in this shared pool.", use.Key))
			} else {
				v.reasons = append(v.reasons, poolReason("capacity_unknown", "Capacity is unknown or stale; one bounded attempt is eligible from this snapshot.", use.Key))
			}
		}
	}
	if v.rank.Continuity {
		v.reasons = append(v.reasons, reason("continuity", "The eligible route preserves the previous connection and runtime at this task boundary."))
	}
	return v, out
}

func selectEffort(c Candidate, requested string) (Effort, bool) {
	if len(c.Efforts) == 0 {
		return Effort{Level: "default"}, requested == "auto"
	}
	if requested == "max" {
		return c.Efforts[len(c.Efforts)-1], true
	}
	if requested == "auto" {
		requested = c.DefaultEffort
	}
	for _, e := range c.Efforts {
		if e.Level == requested {
			return e, true
		}
	}
	return Effort{}, false
}

func supports(c Capability) bool {
	return c.Support == "native" || c.Support == "mapped" || c.Support == "conditional" && c.ConditionsMet
}

func fresh(at, now time.Time, maxAge time.Duration) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= maxAge
}
func reason(code, msg string) Reason { return Reason{Code: code, Message: msg} }
func poolReason(code, msg string, key PoolKey) Reason {
	return Reason{Code: code, Message: msg, Pool: &key}
}

func less(a, b RankedCandidate, p Preference) bool {
	if a.Continuity != b.Continuity {
		return a.Continuity
	}
	latency := func() int {
		if a.Latency == b.Latency {
			return 0
		}
		if a.Latency == 0 {
			return 1
		}
		if b.Latency == 0 {
			return -1
		}
		if a.Latency < b.Latency {
			return -1
		}
		return 1
	}
	cost := func() int {
		if a.Cost.Amount < b.Cost.Amount {
			return -1
		}
		if a.Cost.Amount > b.Cost.Amount {
			return 1
		}
		return 0
	}
	if p == Quality && a.QualityClass != b.QualityClass {
		return a.QualityClass > b.QualityClass
	}
	if p == Faster {
		if v := latency(); v != 0 {
			return v < 0
		}
	}
	if p == Conserve {
		if v := cost(); v != 0 {
			return v < 0
		}
	}
	if a.FirstUse != b.FirstUse {
		return !a.FirstUse
	}
	if a.CapacityKnown != b.CapacityKnown {
		return a.CapacityKnown
	}
	if p == Conserve {
		if a.Pressure != nil && b.Pressure != nil && *a.Pressure != *b.Pressure {
			return *a.Pressure < *b.Pressure
		}
	}
	if v := latency(); v != 0 {
		return v < 0
	}
	if v := cost(); v != 0 {
		return v < 0
	}
	return a.CandidateID < b.CandidateID
}

func rankingDescription(p Preference) string {
	order := "verified entitlement, known capacity, known completion latency, marginal cost"
	if p == Faster {
		order = "known completion latency, verified entitlement, known capacity, marginal cost"
	}
	if p == Quality {
		order = "evaluated quality class, verified entitlement, known capacity, known completion latency, marginal cost"
	}
	if p == Conserve {
		order = "marginal cost, verified entitlement, known capacity, largest estimated share of usable pool headroom, known completion latency"
	}
	return string(p) + " ranks eligible routes by continuity, " + order + ", then stable route ID."
}

func validateRequest(r Request) error {
	bad := func(msg string) error { return core.Problem("invalid_routing_snapshot", msg, 2) }
	if err := core.ValidateSelection(r.Requested); err != nil {
		return err
	}
	if r.Now.IsZero() || r.MaxCatalogAge < 0 || r.MaxQuotaAge < 0 {
		return bad("Supply a current snapshot time and nonnegative freshness limits.")
	}
	if r.Workspace.ID == "" || r.Workspace.Root == "" || r.Workspace.Dir == "" {
		return bad("Supply the resolved workspace identity, root and directory.")
	}
	if len(r.AllowedAccounts) == 0 || len(r.AllowedAccounts) > 4096 || len(r.Candidates) > 4096 || len(r.Pools) > 8192 {
		return bad("Supply an explicit bounded account set and bounded candidate/pool snapshots.")
	}
	if r.MinQuality < 0 || r.ContextRequired < 0 || len(r.RequiredCapabilities) > 128 || len(r.RequiredPermissions) > 32 {
		return bad("Task requirements must be nonnegative and bounded.")
	}
	if r.Preference != "" && r.Preference != Balanced && r.Preference != Faster && r.Preference != Conserve && r.Preference != Quality {
		return bad("Choose balanced, faster, conserve or quality.")
	}
	if r.MaxCost != nil && !validMoney(r.MaxCost) {
		return bad("The spend ceiling requires a currency and finite nonnegative amount.")
	}
	for _, id := range r.AllowedAccounts {
		if !identifier(id) || id == "auto" {
			return bad("Allowed accounts must be exact nonempty connection IDs.")
		}
	}
	for _, name := range r.RequiredCapabilities {
		if !identifier(name) {
			return bad("Required capabilities must have bounded nonempty IDs.")
		}
	}
	for dimension, value := range r.RequiredPermissions {
		if !identifier(dimension) || !identifier(value) {
			return bad("Permission dimensions and values must be nonempty identifiers.")
		}
		if dimension == "filesystem" && value != r.Requested.Permission {
			return bad("The filesystem requirement conflicts with the requested permission envelope.")
		}
	}
	seen := map[PoolKey]bool{}
	for _, p := range r.Pools {
		if !validKey(p.Key) || !identifier(p.Unit) || seen[p.Key] || !number(p.Reserve) || !number(p.LocalReserved) || p.Inflight < 0 || p.Remaining != nil && !number(*p.Remaining) {
			return bad("Pool snapshots need unique scope/bucket keys, exact units and finite nonnegative values.")
		}
		if p.Unmetered && (p.Remaining != nil || p.Reserve != 0 || p.LocalReserved != 0 || p.Blocked) {
			return bad("An unmetered allowance cannot also declare metered capacity or exhaustion.")
		}
		seen[p.Key] = true
	}
	ids := map[string]bool{}
	for _, c := range r.Candidates {
		if !identifier(c.ID) || ids[c.ID] || !identifier(c.Route.Runtime) || !identifier(c.Route.Provider) || !identifier(c.Route.Model) || c.Route.Model == "auto" || !identifier(c.Route.Account) || c.Route.Account == "auto" || !identifier(c.Route.Mode) || !identifier(c.AuthMode) {
			return bad("Candidates need unique IDs and exact runtime, provider, model, connection, mode and auth identities.")
		}
		ids[c.ID] = true
		if c.QualityClass < 0 || c.ContextLimit < 0 || c.Latency < 0 || len(c.Capabilities) > 128 || len(c.Permissions) > 32 {
			return bad("Candidate metadata must be bounded and nonnegative.")
		}
		if !oneOf(c.Entitlement, "verified", "advertised", "unknown", "denied", "not-required") || !oneOf(c.Billing, "included", "unmetered", "metered", "unknown") {
			return bad("Unknown entitlement or billing enum; refresh the adapter snapshot.")
		}
		if c.CostUpperBound != nil && (!validMoney(c.CostUpperBound) || c.Billing != "metered" && c.CostUpperBound.Amount != 0) {
			return bad("Cost bounds must be finite, nonnegative and consistent with the billing class.")
		}
		for name, cap := range c.Capabilities {
			if !identifier(name) || !validSupport(cap.Support) {
				return bad("Invalid capability evidence.")
			}
		}
		for dim, values := range c.Permissions {
			if !identifier(dim) || len(values) > 32 {
				return bad("Invalid permission dimension.")
			}
			for value, cap := range values {
				if !identifier(value) || !validSupport(cap.Support) {
					return bad("Invalid permission evidence.")
				}
			}
		}
		previous := 0
		for _, e := range c.Efforts {
			n := effortOrder(e.Level)
			if n <= previous || !identifier(e.Native) {
				return bad("Effort mappings must be distinct, ascending low/medium/high/max with exact native values.")
			}
			previous = n
		}
		if len(c.Efforts) == 0 && c.DefaultEffort != "" {
			return bad("An effort-free route cannot declare a default effort mapping.")
		}
		if len(c.Efforts) > 0 {
			found := false
			for _, e := range c.Efforts {
				found = found || e.Level == c.DefaultEffort
			}
			if !found {
				return bad("The auto effort default must be an admitted mapping.")
			}
		}
		if len(c.Pools) == 0 || len(c.Pools) > 32 {
			return bad("Each route must identify all applicable pools, including an explicit unknown or unmetered pool.")
		}
		uses := map[PoolKey]bool{}
		for _, u := range c.Pools {
			if !validKey(u.Key) || !identifier(u.Unit) || uses[u.Key] || u.Estimate != nil && !number(*u.Estimate) {
				return bad("Pool uses must be distinct and have exact units and finite nonnegative estimates.")
			}
			uses[u.Key] = true
		}
	}
	return nil
}

func validKey(k PoolKey) bool { return identifier(k.Scope) && identifier(k.Bucket) }
func identifier(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func number(n float64) bool    { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
func validMoney(m *Money) bool { return identifier(m.Currency) && number(m.Amount) }
func validSupport(s string) bool {
	return oneOf(s, "native", "mapped", "conditional", "unsupported", "unverified")
}
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func effortOrder(s string) int {
	switch s {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "max":
		return 4
	}
	return 0
}
