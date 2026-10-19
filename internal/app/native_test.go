package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/state"
	"ucli.local/ucli/internal/workspace"

	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

func nativeFixture(now time.Time) []connections.Connection {
	return []connections.Connection{
		{ID: "conn-personal", Label: "personal", Provider: "codex", Executable: "native-a", SHA256: strings.Repeat("a", 64), NativeHome: "home-a", Identity: "opaque-a", AuthMode: "chatgpt", AuthState: "authenticated", ObservedAt: now,
			Models: []connections.Model{{ID: "fast", Name: "Fast", Efforts: []string{"low", "medium"}, DefaultEffort: "medium", Default: true}, {ID: "deep", Name: "Deep", Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "high"}}},
		{ID: "conn-work", Label: "work", Provider: "codex", Executable: "native-b", SHA256: strings.Repeat("b", 64), NativeHome: "home-b", Identity: "opaque-b", AuthMode: "chatgpt", AuthState: "authenticated", ObservedAt: now,
			Models: []connections.Model{{ID: "deep", Name: "Deep", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high", Default: true}}},
	}
}

func TestNativeRouteKeepsPinsAndFreezesBinding(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	items := nativeFixture(now)
	selection := core.Selection{Model: "codex/deep", Account: "work", Effort: "max", Permission: "sandbox"}
	route, executable, binding, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, selection, items, now)
	if err != nil {
		t.Fatal(err)
	}
	if route.Model != "codex/deep" || route.Account != "conn-work" || route.Effort != "high" || route.Permission != "sandbox" || executable != "native-b" || binding.Home != "home-b" {
		t.Fatalf("explicit intent changed: %+v %+v", route, binding)
	}
	items[1].NativeHome = "changed-later"
	if binding.Home != "home-b" {
		t.Fatal("binding was not copied before acceptance")
	}
	if selection.Account != "work" || selection.Effort != "max" {
		t.Fatal("requested intent was mutated")
	}
}

func TestNativeRouteRejectsChangedIdentityAndUnsupportedAuthority(t *testing.T) {
	now := time.Now().UTC()
	for _, mutate := range []func(*core.Selection, []connections.Connection){
		func(s *core.Selection, c []connections.Connection) { c[1].AuthState = "identity-changed" },
		func(s *core.Selection, c []connections.Connection) { s.Permission = "workspace-write" },
		func(s *core.Selection, c []connections.Connection) { s.Model = "codex/missing" },
		func(s *core.Selection, c []connections.Connection) {
			s.Model = "auto"
			c[1].ObservedAt = now.Add(-48 * time.Hour)
		},
	} {
		items := nativeFixture(now)
		selection := core.Selection{Model: "codex/deep", Account: "work", Effort: "auto", Permission: "read-only"}
		mutate(&selection, items)
		if _, _, _, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, selection, items, now); err == nil {
			t.Fatal("unavailable pinned route fell back to another account/permission")
		}
	}
}

func TestNativeAutoConservesEffortWithoutClaimingKnownQuota(t *testing.T) {
	now := time.Now().UTC()
	route, _, _, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, core.Defaults(), nativeFixture(now), now)
	if err != nil {
		t.Fatal(err)
	}
	if route.Effort != "low" || route.Permission != "read-only" {
		t.Fatalf("auto expanded effort/authority: %+v", route)
	}
	if !strings.Contains(strings.Join(route.Reasons, " "), "usage is unknown") {
		t.Fatal("unknown usage was hidden")
	}
}

func TestAcceptedNativeLabelCannotFollowReusedConnectionName(t *testing.T) {
	now := time.Now().UTC()
	items := nativeFixture(now)
	w := core.Workspace{ID: "workspace", Root: "root", Dir: "root"}
	selection := core.Selection{Model: "codex/deep", Account: "work", Effort: "low", Permission: "read-only"}
	route, _, _, err := planNativeRoute(w, selection, items, now)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cmd := core.RunCommand{ID: "command-label", Input: "synthetic no-inference task", Requested: selection, Effective: route, Workspace: w, Owner: core.Owner{ID: "owner", PID: 123, StartedAt: now}}
	accepted, _, err := store.Accept(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.GetTask(context.Background(), accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	items[1].ID = "conn-new-account"
	items[1].Identity = "different-identity"
	items[1].NativeHome = "different-home"
	if _, _, _, err = planNativeRoute(w, task.Selection, items, now); err == nil {
		t.Fatal("follow-up silently switched to the account reusing the label")
	}
	explicit := task.Selection
	explicit.Account = "work"
	newRoute, _, _, err := planNativeRoute(w, explicit, items, now)
	if err != nil || newRoute.Account != "conn-new-account" {
		t.Fatal("explicit account change should remain possible", err)
	}
	if accepted.Requested.Account != "work" || accepted.Effective.Account != "conn-work" {
		t.Fatal("original receipt changed")
	}
}

func TestNativeUnknownDiagnosticPersistsThroughSharedExecutor(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	requested := core.Defaults()
	requested.Account = "connection-original"
	route := core.Route{Runtime: "codex", Provider: "openai", Model: "codex/synthetic", Account: requested.Account, Effort: "low", Permission: "read-only", Mode: "native"}
	cmd := core.RunCommand{ID: core.NewID("command"), Input: "synthetic diagnostic persistence", Requested: requested, Effective: route, Workspace: w, Owner: a.owner}
	accepted, _, err := a.Store.Accept(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	reason := "Codex did not acknowledge a valid active turn"
	driver := driverFunc(func(context.Context, adapters.Request, func(string) error) (adapters.Outcome, error) {
		return adapters.Outcome{State: "unknown", Message: "Codex turn acceptance or terminal outcome is unknown. " + reason, NativeModel: "synthetic"}, errors.New(reason)
	})
	got, runErr := a.runAccepted(ctx, accepted, "", driver, nil)
	if runErr == nil || got.State != "unknown" || !strings.Contains(got.Error, reason) {
		t.Fatalf("safe diagnostic lost at app boundary: %+v %v", got, runErr)
	}
	saved, hash, err := a.Store.GetCommand(ctx, cmd.ID)
	if err != nil || hash != cmd.IntentHash() || saved.State != "unknown" || !strings.Contains(saved.Error, reason) {
		t.Fatal("durable receipt lost diagnostic", err)
	}
	replay, err := a.Run(ctx, Options{Input: cmd.Input, CWD: cwd, CommandID: cmd.ID, Selection: requested}, nil)
	if err == nil || replay.ID != got.ID || !strings.Contains(replay.Error, reason) {
		t.Fatal("replay lost unknown diagnostic", err)
	}
	events, err := a.Store.Events(ctx, got.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	observed := false
	for _, e := range events {
		if e.Type == "status" && strings.Contains(e.Text, "Native reported model: synthetic") {
			observed = true
		}
	}
	if !observed {
		t.Fatal("observed native model was not retained")
	}
}
