package app

import (
	"context"
	"strings"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/adapters/claude"
	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/routing"
)

func (a *App) LinkedConnections(ctx context.Context) ([]connections.Connection, error) {
	svc, err := connections.New(a.Home)
	if err != nil {
		return nil, err
	}
	return svc.List(ctx)
}

func universalModelID(provider, id string) string {
	if strings.HasPrefix(id, provider+"/") {
		return id
	}
	return provider + "/" + id
}

func publicEfforts(levels []string) []routing.Effort {
	seen := make(map[string]bool, len(levels))
	for _, value := range levels {
		seen[value] = true
	}
	var result []routing.Effort
	for _, level := range []string{"low", "medium", "high", "max"} {
		if seen[level] {
			native := level
			if level == "max" {
				native = "highest-supported"
			}
			result = append(result, routing.Effort{Level: level, Native: native})
		}
	}
	if seen["xhigh"] && !seen["max"] {
		result = append(result, routing.Effort{Level: "max", Native: "xhigh"})
	}
	return result
}

// Cached catalogs keep startup and selection offline. Dispatch independently
// verifies the connection, executable, native account and exact requested model.
func (a *App) nativeModels() []core.Model {
	items, err := a.LinkedConnections(context.Background())
	if err != nil {
		return nil
	}
	return nativeCatalogModels(items)
}

func (a *App) resolveNativeRoute(ctx context.Context, opt Options) (core.Route, string, *adapters.NativeBinding, error) {
	fail := func(err error) (core.Route, string, *adapters.NativeBinding, error) {
		return core.Route{}, "", nil, err
	}
	w, task, err := a.resolveWorkspace(ctx, opt.TaskID, opt.CWD)
	if err != nil {
		return fail(err)
	}
	selection := inherit(task.Selection, opt.Selection)
	if err = core.ValidateSelection(selection); err != nil {
		return fail(err)
	}
	items, err := a.LinkedConnections(ctx)
	if err != nil {
		return fail(err)
	}
	return planNativeRoute(w, selection, items, time.Now().UTC())
}

func planNativeRoute(w core.Workspace, selection core.Selection, items []connections.Connection, now time.Time) (core.Route, string, *adapters.NativeBinding, error) {
	fail := func(err error) (core.Route, string, *adapters.NativeBinding, error) {
		return core.Route{}, "", nil, err
	}
	if len(items) == 0 {
		return fail(core.Problem("provider_not_ready", "Connect a native account with ucli login codex or ucli login claude. Use --demo for the local task loop.", 3))
	}
	requested := selection
	if selection.Account != "auto" {
		var found []connections.Connection
		for _, item := range items {
			if item.ID == selection.Account {
				found = []connections.Connection{item}
				break
			}
			if item.Label == selection.Account {
				found = append(found, item)
			}
		}
		if len(found) != 1 {
			return fail(core.Problem("account_unavailable", "Choose an unambiguous linked account ID from ucli accounts.", 3))
		}
		requested.Account = found[0].ID
		items = found
	}
	var candidates []routing.Candidate
	var allowed []string
	var pools []routing.PoolSnapshot
	bindings := map[string]adapters.NativeBinding{}
	seenPools := map[string]bool{}
	for _, item := range items {
		if item.AuthState != "authenticated" || item.Identity == "" || item.SHA256 == "" || item.Compatibility == "unknown" {
			continue
		}
		provider := ""
		if item.Provider == "codex" && item.AuthMode == "chatgpt" {
			provider = "openai"
		}
		if item.Provider == "claude" && item.AuthMode == "claude.ai" {
			provider = "anthropic"
		}
		if provider == "" {
			continue
		}
		allowed = append(allowed, item.ID)
		pool := routing.PoolKey{Scope: "native:" + item.Provider, Bucket: "unknown-native-allowance"}
		if !seenPools[item.Provider] {
			pools = append(pools, routing.PoolSnapshot{Key: pool, Unit: "native-turns", ObservedAt: now})
			seenPools[item.Provider] = true
		}
		models := item.Models
		if item.Provider == "claude" && requested.Model != "auto" {
			converted := make([]claude.Model, 0, len(models))
			for _, m := range models {
				converted = append(converted, claude.Model{ID: m.ID, ResolvedID: m.ResolvedID, EffortState: m.EffortState, Efforts: m.Efforts, Unavailable: m.Unavailable})
			}
			chosen, err := claude.ResolveModel(converted, strings.TrimPrefix(requested.Model, "claude/"))
			if err != nil {
				continue
			}
			models = nil
			for _, m := range item.Models {
				if m.ID == chosen.ID {
					models = []connections.Model{m}
					break
				}
			}
		}
		for _, model := range models {
			if nativeModelLimitation(item, model) != "" {
				continue
			}
			id := universalModelID(item.Provider, model.ID)
			if requested.Model == "auto" && !model.Default {
				continue
			}
			if requested.Model != "auto" && item.Provider == "claude" && model.ResolvedID != "" && requested.Model == universalModelID(item.Provider, model.ResolvedID) {
				id = requested.Model
			}
			if requested.Model != "auto" && requested.Model != id {
				continue
			}
			efforts := nativePublicEfforts(item.Provider, model.Efforts)
			if item.Provider == "claude" && requested.Effort == "max" {
				ranked := false
				for _, effort := range efforts {
					ranked = ranked || effort.Level == "max"
				}
				if !ranked {
					continue
				}
			}
			defaultEffort := model.DefaultEffort
			if defaultEffort == "xhigh" {
				defaultEffort = "max"
			}
			if len(efforts) > 0 {
				// Conserve quota for ordinary auto tasks until task-based routing has
				// measured quality evidence. Explicit effort pins are never lowered.
				defaultEffort = efforts[0].Level
			}
			candidateID := item.ID + "/" + id
			bindings[candidateID] = item.Binding()
			candidates = append(candidates, routing.Candidate{ID: candidateID,
				Route: core.Route{Runtime: item.Provider, Provider: provider, Model: id, Account: item.ID, Mode: "native"}, Workspace: w,
				Admitted: true, Available: true, WorkspaceAllowed: true, AuthMode: item.AuthMode, Authenticated: true, BindingVerified: true, PrimaryModelFixed: true,
				Advertised: true, CatalogObservedAt: item.ObservedAt, Entitlement: "unknown", Billing: "included", Efforts: efforts, DefaultEffort: defaultEffort,
				Permissions: map[string]map[string]routing.Capability{"filesystem": {"read-only": {Support: "mapped"}, "sandbox": {Support: "mapped"}}},
				Pools:       []routing.PoolUse{{Key: pool, Unit: "native-turns"}}})
		}
	}
	if len(candidates) == 0 {
		return fail(core.Problem("native_route_unavailable", "No linked native account matches this model/account. Refresh accounts status, select a listed model, or finish native sign-in. No alternate account or API key was used.", 3))
	}
	decision, err := routing.Resolve(routing.Request{Requested: requested, Workspace: w, AllowedAccounts: allowed, Candidates: candidates, Pools: pools, Now: now,
		AllowFirstUse: true, BoundedAttempt: true, Preference: routing.Conserve})
	if err != nil {
		if len(decision.ExcludedReasons) > 0 && len(decision.ExcludedReasons[0].Reasons) > 0 {
			return fail(core.Problem("native_route_policy", decision.ExcludedReasons[0].Reasons[0].Message+" Refresh accounts status for stale catalogs; sandbox explicitly enables full local access.", problemExit(err)))
		}
		return fail(err)
	}
	binding := bindings[decision.CandidateID]
	route := decision.Effective
	route.Reasons = append([]string{"Linked native account; fresh identity/model/permission checks precede dispatch. Native subscription controls apply; API-key fallback is disabled.", "Remaining native usage is unknown. One active or uncertain attempt per provider per UCLI home is enforced at acceptance."}, route.Reasons...)
	if route.Permission == "sandbox" {
		route.Reasons = append(route.Reasons, "Sandbox explicitly authorizes full local computer access through native tools; this mode adds no OS isolation.")
	} else if route.Permission == "read-only" {
		route.Reasons = append(route.Reasons, "Read-only currently runs with native tools disabled and text input only. Choose sandbox explicitly when the agent needs local files or commands.")
	}
	return route, binding.Executable, &binding, nil
}

func problemExit(err error) int {
	if problem, ok := err.(*core.Error); ok {
		return problem.Exit
	}
	return 5
}

func nativePublicEfforts(provider string, levels []string) []routing.Effort {
	if provider == "claude" {
		return publicEfforts(claude.PublicEfforts(levels))
	}
	// Codex Inspect already returns public levels, preserving its private native
	// mapping for fresh dispatch. AGY catalogs have no effort admission here.
	return publicEfforts(levels)
}
