package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workspace"
)

// Only the test executable accepts this fixture. No production test/crash flag is
// exposed. The accepted intent is real state; no executor or provider is started.
func TestAppFixtureProcess(t *testing.T) {
	if os.Getenv("UCLI_APP_FIXTURE") != "accepted-orphan" {
		return
	}
	a, err := Open(os.Getenv("UCLI_APP_FIXTURE_HOME"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := workspace.Resolve(os.Getenv("UCLI_APP_FIXTURE_CWD"))
	if err != nil {
		t.Fatal(err)
	}
	attempt, _, err := a.Store.Accept(context.Background(), core.RunCommand{ID: "fixture-orphan", Input: "Interrupted local demonstration", Requested: core.Defaults(),
		Effective: core.Route{Runtime: "demo", Provider: "local", Model: "demo/fast", Account: "demo", Effort: "medium", Permission: "read-only", Mode: "demo"}, Workspace: w, Owner: a.owner})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(attempt); err != nil {
		t.Fatal(err)
	}
	os.Exit(17)
}

func TestAcceptedIntentSurvivesOwnerDeathAndRecoversWithoutReplay(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "workspace")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAppFixtureProcess$")
	cmd.Env = append(os.Environ(), "UCLI_APP_FIXTURE=accepted-orphan", "UCLI_APP_FIXTURE_HOME="+home, "UCLI_APP_FIXTURE_CWD="+cwd)
	hideFixture(cmd)
	output, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 {
		t.Fatalf("fixture failed: %v %s", err, output)
	}
	var original core.Attempt
	if err = json.Unmarshal(output, &original); err != nil {
		t.Fatal(err)
	}
	a, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	task, history, err := a.Task(context.Background(), original.TaskID)
	if err != nil || len(history) != 1 || history[0].State != "running" {
		t.Fatal("intent disappeared or claimed completion", task, history, err)
	}
	reconciled, err := a.Recover(context.Background(), original.TaskID)
	if err != nil || reconciled.State != "interrupted" {
		t.Fatal("safe demo reconciliation failed", reconciled, err)
	}
	_, history, err = a.Task(context.Background(), original.TaskID)
	if err != nil || len(history) != 1 {
		t.Fatal("recovery replayed work", history, err)
	}
	next, err := a.Run(context.Background(), Options{TaskID: original.TaskID, Input: "Explicit next work", Demo: true}, nil)
	if err != nil || next.ID == original.ID || next.State != "complete" {
		t.Fatal(next, err)
	}
}

func TestRecoveryRefusesLiveOwnerAndRemoteAssumptions(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	attempt, _, err := a.Store.Accept(ctx, core.RunCommand{ID: "active-owner", Input: "in flight", Requested: core.Defaults(), Workspace: w, Owner: a.owner,
		Effective: core.Route{Runtime: "demo", Provider: "local", Model: "demo/fast", Account: "demo", Effort: "medium", Permission: "read-only", Mode: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Recover(ctx, attempt.TaskID); err == nil {
		t.Fatal("took work from an active owner")
	}
	if _, err = a.Store.Finish(ctx, attempt.ID, a.owner.ID, "interrupted", "", "test cleanup"); err != nil {
		t.Fatal(err)
	}
	owner := core.Owner{ID: "fixture-dead-owner", PID: 99999999, StartedAt: time.Now().UTC()}
	remote, _, err := a.Store.Accept(ctx, core.RunCommand{ID: "uncertain-remote", Input: "remote fixture", Requested: core.Defaults(), Workspace: w, Owner: owner,
		Effective: core.Route{Runtime: "external-fixture", Provider: "fixture", Model: "fixture", Account: "fixture", Effort: "medium", Permission: "read-only", Mode: "managed"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Recover(ctx, remote.TaskID); err == nil {
		t.Fatal("process death treated as remote stop proof")
	}
}
