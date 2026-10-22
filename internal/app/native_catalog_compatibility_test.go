package app

import (
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

func TestNativeCatalogCompatibilityGatesBeforeAcceptance(t *testing.T) {
	now := time.Now().UTC()
	base := connections.Connection{ID: "claude-fixture", Provider: "claude", AuthState: "authenticated", AuthMode: "claude.ai", Identity: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), Compatibility: "reviewed", ObservedAt: now, Models: []connections.Model{{ID: "new-native-alias", Name: "Native new model", ResolvedID: "claude-wire-new", EffortState: "supported", Efforts: []string{"low", "xhigh"}, Default: true}}}
	for _, mode := range []string{"ready", "drift", "unresolved", "unknown-effort", "fixed-effort", "disabled", "future-effort"} {
		t.Run(mode, func(t *testing.T) {
			c := base
			c.Models = append([]connections.Model{}, base.Models...)
			switch mode {
			case "drift":
				c.Compatibility = "unknown"
			case "unresolved":
				c.Models[0].ResolvedID = ""
			case "unknown-effort":
				c.Models[0].EffortState = "unknown"
			case "fixed-effort":
				c.Models[0].EffortState = "unsupported"
				c.Models[0].Efforts = nil
			case "disabled":
				c.Models[0].Unavailable = true
			case "future-effort":
				c.Models[0].Efforts = []string{"future"}
			}
			models := nativeCatalogModels([]connections.Connection{c})
			if len(models) != 1 || models[0].ID != "claude/new-native-alias" || models[0].Available != (mode == "ready") {
				t.Fatalf("bad visible capability: %+v", models)
			}
			selection := core.Defaults()
			selection.Model = "claude/new-native-alias"
			selection.Account = c.ID
			_, _, _, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, selection, []connections.Connection{c}, now)
			if (err == nil) != (mode == "ready") {
				t.Fatalf("route %s err=%v", mode, err)
			}
			if mode == "fixed-effort" && !strings.Contains(models[0].Reason, "fixed effort") {
				t.Fatal("fixed and unknown effort conflated")
			}
		})
	}
	selection := core.Defaults()
	selection.Model = "claude/claude-wire-new"
	selection.Account = base.ID
	route, _, _, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, selection, []connections.Connection{base}, now)
	if err != nil || route.Model != selection.Model {
		t.Fatalf("exact native pin changed to alias: %+v %v", route, err)
	}
}

func TestExactClaudeModelAliasDuplicatesAndConflicts(t *testing.T) {
	now := time.Now().UTC()
	m := connections.Model{ID: "default", Name: "Default", ResolvedID: "claude-same", EffortState: "supported", Efforts: []string{"low", "high"}, Default: true}
	alias := m
	alias.ID = "another"
	alias.Default = false
	c := connections.Connection{ID: "account", Provider: "claude", AuthState: "authenticated", AuthMode: "claude.ai", Identity: "opaque", SHA256: strings.Repeat("a", 64), Compatibility: "reviewed", ObservedAt: now, Models: []connections.Model{m, alias}}
	selection := core.Defaults()
	selection.Account = c.ID
	selection.Model = "claude/claude-same"
	w := core.Workspace{ID: "workspace", Root: "root", Dir: "root"}
	for _, models := range [][]connections.Model{{m, alias}, {alias, m}} {
		c.Models = models
		r, _, _, err := planNativeRoute(w, selection, []connections.Connection{c}, now)
		if err != nil || r.Model != selection.Model {
			t.Fatalf("compatible aliases duplicated: %+v %v", r, err)
		}
	}
	for _, change := range []func(*connections.Model){func(m *connections.Model) { m.Unavailable = true }, func(m *connections.Model) { m.EffortState = "unknown" }, func(m *connections.Model) { m.Efforts = []string{"low"} }} {
		bad := alias
		change(&bad)
		c.Models = []connections.Model{bad, m}
		if _, _, _, err := planNativeRoute(w, selection, []connections.Connection{c}, now); err == nil {
			t.Fatal("conflicting wire-ID aliases selected")
		}
	}
	c.Models = []connections.Model{m}
	c.Models[0].Efforts = []string{"low", "future"}
	selection.Model = "claude/default"
	selection.Effort = "low"
	if _, _, _, err := planNativeRoute(w, selection, []connections.Connection{c}, now); err != nil {
		t.Fatal("known low lost on additive native effort", err)
	}
	selection.Effort = "max"
	if _, _, _, err := planNativeRoute(w, selection, []connections.Connection{c}, now); err == nil {
		t.Fatal("unknown native rank guessed as max")
	}
}
