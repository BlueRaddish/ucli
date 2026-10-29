package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

func TestUnsupportedNativePermissionNamesRecoveryWithoutRefresh(t *testing.T) {
	for _, runtime := range []string{"codex", "claude"} {
		for _, permission := range []string{"workspace-write", "unrestricted"} {
			t.Run(runtime+"/"+permission, func(t *testing.T) {
				now := time.Now().UTC()
				selection := core.Selection{Model: runtime + "/fast", Account: "conn-personal", Effort: "low", Permission: permission}
				before := selection
				refreshes := 0
				_, _, binding, err := planNativeRouteWithRefresh(context.Background(), core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, selection, nativeFixture(now.Add(-48*time.Hour)), now,
					func(context.Context, string) (connections.Connection, error) {
						refreshes++
						return connections.Connection{}, nil
					})
				var problem *core.Error
				if !errors.As(err, &problem) || problem.Code != "unsupported_permission" || problem.Exit != 5 || problem.Acceptance != "not-dispatched" || problem.SideEffects != "none" {
					t.Fatalf("unsupported pin lost its safe recovery: %v", err)
				}
				for _, text := range []string{permission, "/permission", "read-only", "sandbox", "full local computer access"} {
					if !strings.Contains(problem.Message, text) {
						t.Fatalf("missing recovery detail %q: %s", text, problem.Message)
					}
				}
				name := "Codex"
				if runtime == "claude" {
					name = "Claude Code"
				}
				if !strings.Contains(problem.Message, name) || refreshes != 0 || binding != nil || selection != before {
					t.Fatal("permission refusal probed a provider or changed intent")
				}
			})
		}
	}
}

func TestSupportedNativePermissionsKeepExactEnvelope(t *testing.T) {
	now := time.Now().UTC()
	for _, permission := range []string{"read-only", "sandbox"} {
		selection := core.Selection{Model: "codex/fast", Account: "conn-personal", Effort: "low", Permission: permission}
		route, _, binding, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, selection, nativeFixture(now), now)
		if err != nil || route.Permission != permission || route.Account != selection.Account || route.Model != selection.Model || route.Effort != selection.Effort || binding == nil {
			t.Fatalf("supported permission/pins changed: %+v %v", route, err)
		}
	}
}

func TestStartUnsupportedPermissionKeepsRestoredDraftWithoutAttempt(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	selection := core.Selection{Model: "claude/fixture", Account: "pinned", Effort: "high", Permission: "unrestricted"}
	draft, err := a.Draft(ctx, "", "preserved synthetic prompt", cwd, selection)
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.Start(ctx, Options{TaskID: draft.ID, Input: draft.Draft, ExpectedRevision: &draft.Revision})
	var problem *core.Error
	if e != nil || !errors.As(err, &problem) || problem.Code != "unsupported_permission" || problem.TaskID != draft.ID {
		t.Fatalf("restored pin did not stop before native admission: %v", err)
	}
	saved, attempts, err := a.Task(ctx, draft.ID)
	if err != nil || saved.Draft != draft.Draft || saved.Selection != selection || len(attempts) != 0 {
		t.Fatalf("unsupported send lost draft, changed pins or dispatched: %+v %d %v", saved, len(attempts), err)
	}
}

func TestExplicitSandboxRecoveryReachesSyntheticNativeDriverUnchanged(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	requested := core.Selection{Model: "claude/fixture", Account: "conn-claude", Effort: "low", Permission: "unrestricted"}
	draft, err := a.Draft(ctx, "", "synthetic full-local-access recovery", cwd, requested)
	if err != nil {
		t.Fatal(err)
	}
	requested.Permission = "sandbox" // Simulates the separately tested explicit picker confirmation.
	draft, err = a.DraftAt(ctx, draft.ID, draft.Draft, cwd, requested, &draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := []connections.Connection{{ID: requested.Account, Provider: "claude", Executable: "synthetic-native", SHA256: strings.Repeat("a", 64), NativeHome: "synthetic-home", Identity: "opaque-fixture", AuthMode: "claude.ai", AuthState: "authenticated", ObservedAt: now,
		Models: []connections.Model{{ID: "fixture", ResolvedID: "fixture-wire", EffortState: "supported", Efforts: []string{"low"}, Default: true}}}}
	route, executable, binding, err := planNativeRoute(draft.Workspace, requested, items, now)
	if err != nil {
		t.Fatal(err)
	}
	accepted, fresh, err := a.Store.Accept(ctx, core.RunCommand{ID: "sandbox-recovery-fixture", TaskID: draft.ID, ExpectedRevision: draft.Revision, Input: draft.Draft, Requested: requested, Effective: route, Workspace: draft.Workspace, Owner: a.owner})
	if err != nil || !fresh {
		t.Fatalf("synthetic native acceptance: %v", err)
	}
	calls := 0
	driver := driverFunc(func(_ context.Context, req adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		calls++
		if req.Attempt.Requested != requested || req.Attempt.Effective.Permission != "sandbox" || req.Attempt.Effective.Mode != "native" || req.Binding == nil || req.Binding.ID != requested.Account {
			t.Fatal("explicit native permission or independent pins changed before driver")
		}
		if err := req.OnContext(core.NativeContext{Runtime: "claude", Provider: "anthropic", Account: requested.Account, SessionID: "synthetic-sandbox", Durability: "ephemeral", Provenance: "confirmed"}); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		if err := emit("synthetic native sandbox complete"); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		return adapters.Outcome{State: "complete"}, nil
	})
	result, err := a.runAccepted(ctx, accepted, executable, driver, nil, binding)
	if err != nil || calls != 1 || result.State != "complete" || result.Requested != requested || result.Effective.Permission != "sandbox" || result.Output != "synthetic native sandbox complete" {
		t.Fatalf("synthetic native recovery did not settle exactly: %+v %v", result, err)
	}
	_, attempts, err := a.Task(ctx, draft.ID)
	if err != nil || len(attempts) != 1 || attempts[0].State != "complete" || attempts[0].Effective.Permission != "sandbox" {
		t.Fatal("synthetic native result not durable")
	}
}
