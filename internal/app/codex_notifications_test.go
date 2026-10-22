package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workspace"
)

// Layered evidence: codex's owned-child fixtures establish protocol outcomes;
// this public Driver seam checks their durable application lifecycle without
// exporting the private protocol engine or bypassing native admission.
func TestCodexNotificationOutcomeSurvivesRestartAndReplay(t *testing.T) {
	methods := []string{
		"turn/plan/updated", "turn/moderationMetadata", "item/plan/delta",
		"item/commandExecution/terminalInteraction", "item/fileChange/patchUpdated",
		"item/mcpToolCall/progress", "item/autoApprovalReview/started", "item/autoApprovalReview/completed",
		"metadata-then-complete", "metadata-then-failed",
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			a, cwd := fixtureApp(t)
			ctx := context.Background()
			w, err := workspace.Resolve(cwd)
			if err != nil {
				t.Fatal(err)
			}
			requested := core.Defaults()
			requested.Account, requested.Permission = "connection-original", "sandbox"
			route := core.Route{Runtime: "codex", Provider: "openai", Model: "codex/synthetic", Account: requested.Account, Effort: "low", Permission: "sandbox", Mode: "native"}
			cmd := core.RunCommand{ID: core.NewID("command"), Input: "synthetic protocol lifecycle", Requested: requested, Effective: route, Workspace: w, Owner: a.owner}
			accepted, _, err := a.Store.Accept(ctx, cmd)
			if err != nil {
				t.Fatal(err)
			}
			out := adapters.Outcome{State: "unknown", Message: "Codex notification " + method + " was rejected: Codex native detail has no matching active item"}
			var driverErr error = errors.New(out.Message)
			output, wantExit := "", 7
			if method == "metadata-then-complete" {
				out.State, out.Message, output, driverErr, wantExit = "complete", "", "synthetic complete", nil, 0
			} else if method == "metadata-then-failed" {
				out.State, out.Message, wantExit = "failed", "Codex confirmed a failed turn (usageLimitExceeded; quota).", 4
				p := core.Problem("codex_quota_failed", out.Message, 4)
				p.Origin, p.Acceptance, p.SideEffects = "codex", "accepted", "unknown"
				driverErr = p
			}
			calls := 0
			driver := driverFunc(func(_ context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
				calls++
				if output != "" {
					if err := emit(output); err != nil {
						t.Fatal(err)
					}
				}
				return out, driverErr
			})
			got, runErr := a.runAccepted(ctx, accepted, "", driver, nil)
			checkExit := func(err error) {
				t.Helper()
				var p *core.Error
				if wantExit == 0 {
					if err != nil {
						t.Fatal(err)
					}
				} else if !errors.As(err, &p) || p.Exit != wantExit {
					t.Fatalf("wrong durable exit: %v", err)
				}
			}
			checkExit(runErr)
			if got.State != out.State || got.Output != output || got.Error != out.Message {
				t.Fatalf("outcome lost: %+v", got)
			}
			before, err := a.Store.Events(ctx, got.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(a.Home)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			replay, replayErr := reopened.Run(ctx, Options{Input: cmd.Input, CWD: cwd, CommandID: cmd.ID, Selection: requested}, nil)
			checkExit(replayErr)
			if !reflect.DeepEqual(replay, got) || calls != 1 {
				t.Fatal("restart replay changed the receipt or invoked the driver")
			}
			saved, hash, err := reopened.Store.GetCommand(ctx, cmd.ID)
			if err != nil || hash != cmd.IntentHash() || !reflect.DeepEqual(saved, got) {
				t.Fatal("accepted intent changed", err)
			}
			after, err := reopened.Store.Events(ctx, got.ID, 0, 100)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("replay appended or changed evidence", err)
			}
			if out.State == "unknown" {
				if !strings.Contains(saved.Error, method) {
					t.Fatal("safe method label lost")
				}
				next := cmd
				next.ID, next.Owner = core.NewID("command"), reopened.owner
				if _, _, err := reopened.Store.Accept(ctx, next); err == nil {
					t.Fatal("restart released the uncertain provider guard")
				} else {
					var p *core.Error
					if !errors.As(err, &p) || p.Code != "provider_busy" {
						t.Fatal(err)
					}
				}
				// A different runtime bypasses the provider guard, but must still
				// respect the independently retained workspace write lease.
				next.Effective.Runtime = "other-synthetic-runtime"
				if _, _, err := reopened.Store.Accept(ctx, next); err == nil {
					t.Fatal("restart released the uncertain workspace lease")
				} else {
					var p *core.Error
					if !errors.As(err, &p) || p.Code != "workspace_busy" {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
