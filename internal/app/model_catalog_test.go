package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

func TestModelCatalogKeepsUnavailableProvidersDiscoverable(t *testing.T) {
	items := []connections.Connection{{Provider: "agy", AuthState: "unknown", Models: []connections.Model{{ID: "synthetic", Name: "Synthetic AGY"}}}}
	models := nativeCatalogModels(items)
	found := map[string]bool{}
	for _, model := range models {
		found[model.ID] = true
		if model.Available {
			t.Fatalf("unadmitted model became selectable: %+v", model)
		}
	}
	for _, id := range []string{"agy/synthetic", "claude/default", "claude/sonnet", "claude/opus"} {
		if !found[id] {
			t.Fatalf("missing discoverable model %s", id)
		}
	}
	providers := providerCatalog(items, map[string]bool{"claude": true, "agy": true})
	if len(providers) != 4 || providers[1].Installation != "executable-found" || providers[1].Authentication != "not-checked" ||
		providers[2].Integration != "catalog-only" || providers[2].Authentication != "unknown" || providers[3].Integration != "identity-only" {
		t.Fatalf("install/auth/support states conflated: %+v", providers)
	}
}

func TestModelCatalogDuplicateCannotHideAuthenticatedAccount(t *testing.T) {
	items := nativeFixture(time.Now())
	signedOut := items[0]
	signedOut.AuthState = "signed-out"
	items = append([]connections.Connection{signedOut}, items...)
	models := nativeCatalogModels(items)
	for _, model := range models {
		if strings.HasPrefix(model.ID, "codex/") && !model.Available {
			t.Fatalf("earlier signed-out account hid ready model: %+v", model)
		}
	}
	if items[0].AuthState != "signed-out" {
		t.Fatal("catalog changed source connection")
	}
}

func TestCatalogDoesNotAdmitUnsupportedProvidersOrClaudeAliases(t *testing.T) {
	now := time.Now().UTC()
	items := nativeFixture(now)
	items = append(items,
		connections.Connection{ID: "agy", Provider: "agy", AuthMode: "chatgpt", AuthState: "authenticated", Identity: "synthetic", SHA256: strings.Repeat("a", 64), ObservedAt: now, Models: []connections.Model{{ID: "synthetic", Name: "Synthetic"}}},
		connections.Connection{ID: "claude", Provider: "claude", AuthState: "signed-out", Models: []connections.Model{{ID: "sonnet"}}})
	before, _, _, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, core.Defaults(), items[:2], now)
	if err != nil {
		t.Fatal(err)
	}
	after, _, _, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, core.Defaults(), items, now)
	if err != nil || before.Model != after.Model || before.Account != after.Account {
		t.Fatalf("browsing-only routes changed auto choice: %+v %+v %v", before, after, err)
	}
	for _, model := range []string{"agy/synthetic", "claude/sonnet"} {
		selection := core.Defaults()
		selection.Model = model
		if _, _, _, err = planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, selection, items, now); err == nil {
			t.Fatalf("unavailable catalog entry admitted: %s", model)
		}
	}
}

func TestModelCatalogRefusesDamagedMetadataWithoutNativeProbe(t *testing.T) {
	a, _ := fixtureApp(t)
	directory := filepath.Join(a.Home, "connections-v1")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "foreign.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ModelCatalog(context.Background()); err == nil {
		t.Fatal("damaged metadata silently vanished from model picker")
	}
}
