package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

func TestNativeStaleCatalogExplainsRefreshWithoutPermissionAdvice(t *testing.T) {
	now := time.Now().UTC()
	items := nativeFixture(now.Add(-48 * time.Hour))
	_, _, _, err := planNativeRoute(core.Workspace{ID: "workspace", Root: "root", Dir: "root"}, core.Defaults(), items, now)
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != "native_catalog_stale" || !strings.Contains(problem.Message, "model list") || strings.Contains(problem.Message, "sandbox") || strings.Contains(problem.Message, "local access") {
		t.Fatalf("stale catalog confused with permissions: %v", err)
	}
}

func TestNativeCatalogRefreshOnlyForStaleSubmission(t *testing.T) {
	now := time.Now().UTC()
	w := core.Workspace{ID: "workspace", Root: "root", Dir: "root"}
	for _, mode := range []string{"submit", "explain", "fresh", "explicit", "permission"} {
		t.Run(mode, func(t *testing.T) {
			items := nativeFixture(now.Add(-48 * time.Hour))
			selection := core.Defaults()
			calls := 0
			refresh := func(ctx context.Context, id string) (connections.Connection, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok || id != items[0].ID {
					t.Fatal("refresh was unbounded or switched account")
				}
				updated := items[0]
				updated.ObservedAt = now
				return updated, nil
			}
			switch mode {
			case "explain":
				refresh = nil
			case "fresh":
				items = nativeFixture(now)
			case "explicit":
				selection.Model = "codex/fast"
			case "permission":
				selection.Permission = "workspace-write"
			}
			route, _, binding, err := planNativeRouteWithRefresh(context.Background(), w, selection, items, now, refresh)
			if mode == "explain" || mode == "permission" {
				if err == nil || calls != 0 {
					t.Fatal("offline or unsupported request refreshed", err, calls)
				}
				return
			}
			wantCalls := 0
			if mode == "submit" {
				wantCalls = 1
				if !items[0].ObservedAt.Equal(now.Add(-48 * time.Hour)) {
					t.Fatal("caller snapshot mutated")
				}
			}
			if err != nil || calls != wantCalls || route.Account != items[0].ID || binding.Identity != items[0].Identity || route.Permission != "read-only" || route.Effort != "low" {
				t.Fatalf("refresh changed intent or repeated work: %+v %v calls=%d", route, err, calls)
			}
		})
	}
}

func TestNativeCatalogRefreshPreservesAccountAndStopsOnFailure(t *testing.T) {
	now := time.Now().UTC()
	w := core.Workspace{ID: "workspace", Root: "root", Dir: "root"}
	for _, mode := range []string{"pinned", "failure", "identity", "binary", "home", "cancelled", "still-stale"} {
		t.Run(mode, func(t *testing.T) {
			items := nativeFixture(now.Add(-48 * time.Hour))
			selection := core.Defaults()
			selection.Account = "work"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			calls := 0
			sentinel := errors.New("synthetic status failure")
			refresh := func(ctx context.Context, id string) (connections.Connection, error) {
				calls++
				if id != items[1].ID {
					t.Fatal("refreshed another account")
				}
				updated := items[1]
				updated.ObservedAt = now
				switch mode {
				case "failure":
					return updated, sentinel
				case "identity":
					updated.Identity = "another-account"
				case "binary":
					updated.SHA256 = strings.Repeat("c", 64)
				case "home":
					updated.NativeHome = "another-home"
				case "still-stale":
					updated.ObservedAt = items[1].ObservedAt
				}
				return updated, nil
			}
			route, _, _, err := planNativeRouteWithRefresh(ctx, w, selection, items, now, refresh)
			if mode == "pinned" {
				if err != nil || route.Account != items[1].ID || calls != 1 {
					t.Fatal("pinned account was not refreshed exactly once", err, calls)
				}
				return
			}
			wantCalls := 1
			if mode == "cancelled" {
				wantCalls = 0
			}
			if err == nil || calls != wantCalls {
				t.Fatal("refresh failure retried or fell back", err, calls)
			}
			if mode == "failure" && !errors.Is(err, sentinel) || mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("failure category lost", err)
			}
		})
	}
}
