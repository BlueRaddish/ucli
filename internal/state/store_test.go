package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

var background = context.Background()

func openTest(t *testing.T, home string) *Store {
	t.Helper()
	s, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func command(id, workspace, permission string) core.RunCommand {
	selection := core.Defaults()
	selection.Permission = permission
	return core.RunCommand{ID: id, Input: "Synthetic task " + id, Requested: selection,
		Effective: core.Route{Runtime: "demo", Provider: "local", Model: "demo/local", Account: "demo", Effort: "low", Permission: permission, Mode: "demo"},
		Workspace: core.Workspace{ID: workspace, Root: "synthetic-root", Dir: "synthetic-root/src"},
		Owner:     core.Owner{ID: "owner-fixture", PID: 123, StartedAt: time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)}}
}
func code(err error) string {
	var domain *core.Error
	if errors.As(err, &domain) {
		return domain.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
func requireCode(t *testing.T, err error, want string) {
	t.Helper()
	if code(err) != want {
		t.Fatalf("error code=%q want=%q (%v)", code(err), want, err)
	}
}
func accept(t *testing.T, s *Store, cmd core.RunCommand) core.Attempt {
	t.Helper()
	attempt, newly, err := s.Accept(background, cmd)
	if err != nil || !newly {
		t.Fatalf("accept: new=%v error=%v", newly, err)
	}
	return attempt
}

func TestAcceptReplayRestartAndIntentConflict(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	cmd := command("command-a", "workspace-a", "read-only")
	a := accept(t, s, cmd)
	events, err := s.Events(background, a.ID, 0, 100)
	if err != nil || len(events) != 1 || events[0].Type != "accepted" || events[0].Sequence != 1 {
		t.Fatalf("accept event: %+v %v", events, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	replay := cmd
	replay.Owner = core.Owner{}
	replay.Effective = core.Route{}
	b, newly, err := s.Accept(background, replay)
	if err != nil || newly || b.ID != a.ID || b.Effective.Model != a.Effective.Model || b.Owner.ID != a.Owner.ID {
		t.Fatalf("replay changed accepted intent: %+v %v %v", b, newly, err)
	}
	stored, hash, err := s.GetCommand(background, cmd.ID)
	if err != nil || stored.ID != a.ID || hash != cmd.IntentHash() {
		t.Fatalf("lookup: %v %v", stored, err)
	}
	cmd.Input = "changed synthetic input"
	_, _, err = s.Accept(background, cmd)
	requireCode(t, err, "command_conflict")
	tasks, err := s.ListTasks(background, 100, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	_, _, err = s.GetCommand(background, "missing")
	requireCode(t, err, "not_found")
}

func TestDraftCASFollowupAndOldEvidence(t *testing.T) {
	s := openTest(t, t.TempDir())
	cmd := command("first", "workspace-a", "read-only")
	draft, err := s.SaveDraft(background, core.Task{Workspace: cmd.Workspace, Draft: cmd.Input, Selection: cmd.Requested}, 0)
	if err != nil {
		t.Fatal(err)
	}
	stale := draft
	draft.Draft = "edited before dispatch"
	draft, err = s.SaveDraft(background, draft, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveDraft(background, stale, stale.Revision)
	requireCode(t, err, "revision_conflict")
	cmd.TaskID = draft.ID
	cmd.ExpectedRevision = draft.Revision
	a := accept(t, s, cmd)
	_, err = s.Finish(background, a.ID, a.Owner.ID, "complete", "old evidence", "")
	if err != nil {
		t.Fatal(err)
	}
	cmd.ID = "followup"
	cmd.ExpectedRevision = a.Revision
	cmd.Input = "follow up"
	cmd.Workspace.Dir = "synthetic-root/other"
	b := accept(t, s, cmd)
	if b.Revision != a.Revision+1 || b.Workspace.Dir == a.Workspace.Dir {
		t.Fatal("accepted revision/cwd missing")
	}
	history, err := s.Attempts(background, a.TaskID)
	if err != nil || len(history) != 2 || history[0].Output != "old evidence" || history[0].Input == history[1].Input || history[0].ID != a.ID {
		t.Fatalf("old evidence lost: %+v %v", history, err)
	}
	cmd.ID = "bad-workspace"
	cmd.ExpectedRevision = b.Revision
	cmd.Workspace.ID = "another-workspace"
	_, _, err = s.Accept(background, cmd)
	requireCode(t, err, "invalid_input")
}

func TestDraftDuringAttemptSurvivesFinish(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("active", "workspace-a", "read-only"))
	task, err := s.GetTask(background, a.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Draft = "next synthetic input"
	task.Selection.Effort = "high"
	task.Workspace.Dir = "synthetic-root/next"
	task, err = s.SaveDraft(background, task, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Finish(background, a.ID, a.Owner.ID, "complete", "first output", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(background, a.TaskID)
	if err != nil || got.Draft != task.Draft || got.Status != "draft" || got.Selection.Effort != "high" {
		t.Fatalf("draft overwritten: %+v %v", got, err)
	}
	original, err := s.GetAttempt(background, a.ID)
	if err != nil || original.Requested.Effort != "auto" || original.Workspace.Dir != "synthetic-root/src" {
		t.Fatal("accepted evidence changed")
	}
}

func TestWorkspaceLeasesAndUnknownFence(t *testing.T) {
	home := t.TempDir()
	aStore := openTest(t, home)
	bStore := openTest(t, home)
	a := accept(t, aStore, command("reader-a", "workspace-a", "read-only"))
	b := accept(t, bStore, command("reader-b", "workspace-a", "read-only"))
	_, _, err := bStore.Accept(background, command("writer", "workspace-a", "workspace-write"))
	requireCode(t, err, "workspace_busy")
	cmd := command("same-task", "workspace-a", "read-only")
	cmd.TaskID = a.TaskID
	cmd.ExpectedRevision = a.Revision
	_, _, err = aStore.Accept(background, cmd)
	requireCode(t, err, "task_busy")
	if _, err = aStore.Finish(background, a.ID, a.Owner.ID, "unknown", "partial", "lost connection"); err != nil {
		t.Fatal(err)
	}
	if _, err = bStore.Finish(background, b.ID, b.Owner.ID, "complete", "done", ""); err != nil {
		t.Fatal(err)
	}
	aStore.Close()
	aStore = openTest(t, home)
	_, _, err = aStore.Accept(background, command("writer", "workspace-a", "workspace-write"))
	requireCode(t, err, "workspace_busy")
	if _, err = aStore.Reconcile(background, a.ID, a.Owner.ID, false); err != nil {
		t.Fatal(err)
	}
	_, _, err = aStore.Accept(background, command("writer", "workspace-a", "workspace-write"))
	requireCode(t, err, "workspace_busy")
	_, err = aStore.Reconcile(background, a.ID, "wrong-owner", true)
	requireCode(t, err, "owner_mismatch")
	reconciled, err := aStore.Reconcile(background, a.ID, a.Owner.ID, true)
	if err != nil || reconciled.State != "interrupted" || reconciled.Output != "partial" {
		t.Fatalf("reconcile %+v %v", reconciled, err)
	}
	w := accept(t, aStore, command("writer", "workspace-a", "workspace-write"))
	_, _, err = bStore.Accept(background, command("reader-c", "workspace-a", "read-only"))
	requireCode(t, err, "workspace_busy")
	accept(t, bStore, command("other-workspace", "workspace-b", "unrestricted"))
	if _, err = aStore.Finish(background, w.ID, w.Owner.ID, "failed", "", "synthetic failure"); err != nil {
		t.Fatal(err)
	}
	accept(t, bStore, command("reader-c", "workspace-a", "read-only"))
}

func TestSandboxPermissionRetainsExclusiveLeaseAndStoredIntent(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("sandbox-local", "workspace-a", "sandbox"))
	if a.Requested.Permission != "sandbox" || a.Effective.Permission != "sandbox" {
		t.Fatal("sandbox request was silently remapped")
	}
	_, _, err := s.Accept(background, command("reader", "workspace-a", "read-only"))
	requireCode(t, err, "workspace_busy")
	if _, err = s.Finish(background, a.ID, a.Owner.ID, "complete", "done", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckIntegrity(background); err != nil {
		t.Fatal(err)
	}
	accept(t, s, command("reader", "workspace-a", "read-only"))
}

func TestNativeProviderGuardIsAtomicAndRetainsUnknownWork(t *testing.T) {
	home := t.TempDir()
	s, other := openTest(t, home), openTest(t, home)
	cmd := command("native-a", "workspace-a", "read-only")
	cmd.Effective.Runtime, cmd.Effective.Mode = "codex", "native"
	a := accept(t, s, cmd)
	next := command("native-b", "workspace-b", "read-only")
	next.Effective.Runtime, next.Effective.Mode = "codex", "native"
	next.Effective.Account = "another-account"
	_, _, err := other.Accept(background, next)
	requireCode(t, err, "provider_busy")
	if _, err = s.Finish(background, a.ID, a.Owner.ID, "unknown", "", "remote outcome unknown"); err != nil {
		t.Fatal(err)
	}
	_, _, err = other.Accept(background, next)
	requireCode(t, err, "provider_busy")
	different := next
	different.ID, different.Effective.Runtime = "native-other", "claude"
	accept(t, other, different)
	if _, err = s.Reconcile(background, a.ID, a.Owner.ID, true); err != nil {
		t.Fatal(err)
	}
	accept(t, other, next)
}

func TestTerminalAtomicityOwnershipAndEventBounds(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("events", "workspace-a", "read-only"))
	e := core.Event{TaskID: a.TaskID, AttemptID: a.ID, Type: "message_delta", Sequence: 900, Text: "synthetic"}
	_, err := s.AppendEvent(background, e, "other-owner")
	requireCode(t, err, "owner_mismatch")
	saved, err := s.AppendEvent(background, e, a.Owner.ID)
	if err != nil || saved.Sequence != 2 || saved.Version != 1 || saved.At.IsZero() {
		t.Fatalf("event %+v %v", saved, err)
	}
	for _, typ := range []string{"accepted", "terminal_result", "result_persisted", "unknown-type"} {
		e.Type = typ
		_, err = s.AppendEvent(background, e, a.Owner.ID)
		requireCode(t, err, "invalid_input")
	}
	e.Type = "message_delta"
	e.Text = strings.Repeat("x", maxText+1)
	_, err = s.AppendEvent(background, e, a.Owner.ID)
	requireCode(t, err, "invalid_input")
	_, err = s.Finish(background, a.ID, "wrong-owner", "complete", "done", "")
	requireCode(t, err, "owner_mismatch")
	end, err := s.Finish(background, a.ID, a.Owner.ID, "complete", "durable output", "")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Finish(background, a.ID, a.Owner.ID, "complete", "durable output", "")
	if err != nil || !replay.FinishedAt.Equal(end.FinishedAt) {
		t.Fatal("finish replay changed receipt")
	}
	_, err = s.Finish(background, a.ID, a.Owner.ID, "failed", "", "")
	requireCode(t, err, "invalid_state")
	e.Text = "late"
	_, err = s.AppendEvent(background, e, a.Owner.ID)
	requireCode(t, err, "invalid_state")
	events, err := s.Events(background, a.ID, 0, 100)
	if err != nil || len(events) != 4 || events[2].Type != "terminal_result" || events[3].Type != "result_persisted" {
		t.Fatalf("terminal events %+v %v", events, err)
	}
	page, err := s.Events(background, a.ID, 2, 1)
	if err != nil || len(page) != 1 || page[0].Sequence != 3 {
		t.Fatalf("event page %+v %v", page, err)
	}
}

func TestConcurrentEventSequenceAndDraftCAS(t *testing.T) {
	home := t.TempDir()
	left := openTest(t, home)
	right := openTest(t, home)
	a := accept(t, left, command("concurrent", "workspace-a", "read-only"))
	var wg sync.WaitGroup
	errorsOut := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := left
			if i%2 == 0 {
				s = right
			}
			_, err := s.AppendEvent(background, core.Event{AttemptID: a.ID, Type: "message_delta", Text: fmt.Sprint(i)}, a.Owner.ID)
			errorsOut <- err
		}(i)
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := left.Events(background, a.ID, 0, 100)
	if err != nil || len(events) != 41 {
		t.Fatalf("event count=%d error=%v", len(events), err)
	}
	for i, event := range events {
		if event.Sequence != int64(i+1) {
			t.Fatal("sequence gap/duplicate")
		}
	}
	task, err := left.GetTask(background, a.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, s := range []*Store{left, right} {
		go func(s *Store) {
			<-start
			copy := task
			copy.Draft = "new draft"
			_, err := s.SaveDraft(background, copy, task.Revision)
			results <- err
		}(s)
	}
	close(start)
	first, second := <-results, <-results
	if !((first == nil && code(second) == "revision_conflict") || (second == nil && code(first) == "revision_conflict")) {
		t.Fatalf("CAS outcomes %v %v", first, second)
	}
}

func TestSchemaCorruptionCapacityAndReadOnlyFailures(t *testing.T) {
	t.Run("corrupt", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, "state", "state.db")
		os.MkdirAll(filepath.Dir(path), 0700)
		bad := []byte("synthetic not-a-database")
		os.WriteFile(path, bad, 0600)
		_, err := Open(home)
		requireCode(t, err, "state_corrupt")
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, bad) {
			t.Fatal("corrupt evidence overwritten")
		}
	})
	t.Run("newer-open-and-existing-writer", func(t *testing.T) {
		home := t.TempDir()
		s := openTest(t, home)
		if _, err := s.db.Exec("PRAGMA user_version=3"); err != nil {
			t.Fatal(err)
		}
		_, _, err := s.Accept(background, command("blocked", "workspace-a", "read-only"))
		requireCode(t, err, "schema_newer")
		_, err = Open(home)
		requireCode(t, err, "schema_newer")
	})
	t.Run("foreign-schema", func(t *testing.T) {
		home := t.TempDir()
		s := openTest(t, home)
		if _, err := s.db.Exec("PRAGMA user_version=0"); err != nil {
			t.Fatal(err)
		}
		s.Close()
		_, err := Open(home)
		requireCode(t, err, "state_corrupt")
	})
	t.Run("capacity", func(t *testing.T) {
		s := openTest(t, t.TempDir())
		var pages int
		if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
			t.Fatal(err)
		}
		cmd := command("capacity", "workspace-a", "read-only")
		cmd.Input = strings.Repeat("x", maxInput/2)
		_, newly, err := s.Accept(background, cmd)
		if err == nil || newly {
			t.Fatal("capacity failure accepted work")
		}
		_, _, err = s.GetCommand(background, cmd.ID)
		requireCode(t, err, "not_found")
		tasks, err := s.ListTasks(background, 100, 0)
		if err != nil || len(tasks) != 0 {
			t.Fatalf("partial task survived rollback %+v %v", tasks, err)
		}
	})
	t.Run("read-only", func(t *testing.T) {
		s := openTest(t, t.TempDir())
		if _, err := s.db.Exec("PRAGMA query_only=ON"); err != nil {
			t.Fatal(err)
		}
		_, newly, err := s.Accept(background, command("readonly", "workspace-a", "read-only"))
		if err == nil || newly {
			t.Fatal("read-only database accepted work")
		}
	})
	t.Run("invalid-path", func(t *testing.T) {
		_, err := Open("file:///synthetic")
		requireCode(t, err, "invalid_input")
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "state"), []byte("occupied"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err = Open(home)
		requireCode(t, err, "state_unavailable")
	})
}

func TestBusyAndCancelledOperationsDoNotAccept(t *testing.T) {
	home := t.TempDir()
	left := openTest(t, home)
	right := openTest(t, home)
	conn, err := left.db.Conn(background)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(background, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(background, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, newly, err := right.Accept(ctx, command("busy", "workspace-a", "read-only"))
	if err == nil || newly || time.Since(start) > 3*time.Second {
		t.Fatalf("busy not bounded: %v %v", newly, err)
	}
	conn.ExecContext(background, "ROLLBACK")
	_, _, err = right.GetCommand(background, "busy")
	requireCode(t, err, "not_found")
	ctx2, cancel2 := context.WithCancel(background)
	cancel2()
	_, _, err = right.Accept(ctx2, command("cancelled", "workspace-a", "read-only"))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("state lost cancellation identity", err)
	}
	_, _, err = right.GetCommand(background, "cancelled")
	requireCode(t, err, "not_found")
	ctx3, cancel3 := context.WithDeadline(background, time.Now().Add(-time.Second))
	defer cancel3()
	_, _, err = right.Accept(ctx3, command("deadline", "workspace-a", "read-only"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("state lost deadline identity", err)
	}
	_, _, err = right.GetCommand(background, "deadline")
	requireCode(t, err, "not_found")
}

func TestFailedTerminalPersistenceKeepsRunningStateAndLease(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("finish-capacity", "workspace-a", "workspace-write"))
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Finish(background, a.ID, a.Owner.ID, "complete", strings.Repeat("x", maxInput/2), "")
	if err == nil {
		t.Fatal("terminal persistence unexpectedly fit fixed capacity")
	}
	still, err := s.GetAttempt(background, a.ID)
	if err != nil || still.State != "running" || still.Output != "" {
		t.Fatalf("partial finish committed %+v %v", still, err)
	}
	events, err := s.Events(background, a.ID, 0, 100)
	if err != nil || len(events) != 1 || events[0].Type != "accepted" {
		t.Fatalf("partial terminal evidence %+v %v", events, err)
	}
	_, _, err = s.Accept(background, command("conflicting", "workspace-a", "read-only"))
	requireCode(t, err, "workspace_busy")
}

type processResult struct {
	Attempt core.Attempt
	Created bool
	Code    string
}

func TestStateProcessHelper(t *testing.T) {
	if os.Getenv("UCLI_STATE_TEST_HELPER") != "1" {
		return
	}
	s, err := Open(os.Getenv("UCLI_STATE_TEST_HOME"))
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(processResult{Code: code(err)})
		os.Exit(0)
	}
	var cmd core.RunCommand
	if err = json.Unmarshal([]byte(os.Getenv("UCLI_STATE_TEST_COMMAND")), &cmd); err != nil {
		os.Exit(2)
	}
	ready := os.Getenv("UCLI_STATE_TEST_READY")
	barrier := os.Getenv("UCLI_STATE_TEST_BARRIER")
	if err = os.WriteFile(ready, []byte("ready"), 0600); err != nil {
		os.Exit(3)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = os.Stat(barrier); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(4)
		}
		time.Sleep(5 * time.Millisecond)
	}
	a, newly, err := s.Accept(background, cmd)
	json.NewEncoder(os.Stdout).Encode(processResult{Attempt: a, Created: newly, Code: code(err)})
	s.Close()
	os.Exit(0)
}

func TestConcurrentSeparateProcessesReplayAndWriterLease(t *testing.T) {
	for _, sameCommand := range []bool{true, false} {
		t.Run(fmt.Sprint("same-command=", sameCommand), func(t *testing.T) {
			home := t.TempDir()
			barrier := filepath.Join(home, "start")
			base := command("process-race", "workspace-a", "workspace-write")
			children := make([]*exec.Cmd, 2)
			outputs := make([]bytes.Buffer, 2)
			errorsOut := make([]bytes.Buffer, 2)
			for i := range 2 {
				cmd := base
				if !sameCommand {
					cmd.ID += fmt.Sprint(i)
				}
				raw, _ := json.Marshal(cmd)
				child := exec.Command(os.Args[0], "-test.run=^TestStateProcessHelper$")
				hideTestChild(child)
				child.Env = append(os.Environ(), "UCLI_STATE_TEST_HELPER=1", "UCLI_STATE_TEST_HOME="+home, "UCLI_STATE_TEST_COMMAND="+string(raw), "UCLI_STATE_TEST_READY="+filepath.Join(home, fmt.Sprint("ready", i)), "UCLI_STATE_TEST_BARRIER="+barrier)
				child.Stdout = &outputs[i]
				child.Stderr = &errorsOut[i]
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				children[i] = child
				t.Cleanup(func() {
					if child.ProcessState == nil {
						child.Process.Kill()
						child.Wait()
					}
				})
			}
			deadline := time.Now().Add(10 * time.Second)
			for i := range 2 {
				for {
					if _, err := os.Stat(filepath.Join(home, fmt.Sprint("ready", i))); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("child not ready")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			if err := os.WriteFile(barrier, []byte("go"), 0600); err != nil {
				t.Fatal(err)
			}
			results := make([]processResult, 2)
			for i, child := range children {
				done := make(chan error, 1)
				go func() { done <- child.Wait() }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("child: %v %s", err, errorsOut[i].String())
					}
				case <-time.After(10 * time.Second):
					child.Process.Kill()
					<-done
					t.Fatal("child timeout")
				}
				if err := json.Unmarshal(outputs[i].Bytes(), &results[i]); err != nil {
					t.Fatalf("decode %v %q", err, outputs[i].String())
				}
			}
			created := 0
			conflicts := 0
			for _, result := range results {
				if result.Created {
					created++
				}
				if result.Code == "workspace_busy" {
					conflicts++
				}
			}
			if created != 1 {
				t.Fatalf("created=%d results=%+v", created, results)
			}
			if sameCommand {
				if results[0].Code != "" || results[1].Code != "" || results[0].Attempt.ID != results[1].Attempt.ID {
					t.Fatalf("replay mismatch %+v", results)
				}
			} else if conflicts != 1 {
				t.Fatalf("workspace race %+v", results)
			}
		})
	}
}

func TestSQLiteGate(t *testing.T) {
	for _, version := range []string{"3.51.3", "3.52.0", "3.50.7", "3.44.6"} {
		if !fixedSQLite(version) {
			t.Fatal("fixed version rejected", version)
		}
	}
	for _, version := range []string{"3.51.2", "3.50.6", "3.44.5", "bad"} {
		if fixedSQLite(version) {
			t.Fatal("unfixed version accepted", version)
		}
	}
}

func TestFullHistoryHasExplicitByteAndRowBounds(t *testing.T) {
	s := openTest(t, t.TempDir())
	cmd := command("history-0", "workspace-a", "read-only")
	var last core.Attempt
	for i := range 4 {
		cmd.ID = fmt.Sprint("history-", i)
		if i > 0 {
			cmd.TaskID = last.TaskID
			cmd.ExpectedRevision = last.Revision
		}
		last = accept(t, s, cmd)
		var err error
		last, err = s.Finish(background, last.ID, last.Owner.ID, "complete", strings.Repeat("x", core.MaxOutputBytes), "")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Attempts(background, last.TaskID)
	requireCode(t, err, "history_too_large")
	got, err := s.GetAttempt(background, last.ID)
	if err != nil || len(got.Output) != core.MaxOutputBytes {
		t.Fatal("bounded individual receipt unavailable")
	}
	// A row-count overflow is separately rejected before any body is decoded.
	err = s.read(background, func(conn executor) error {
		rows, err := boundedRows(background, conn, "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1001) SELECT '{}' AS body FROM n")
		if rows != nil {
			rows.Close()
		}
		return err
	})
	requireCode(t, err, "history_too_large")
}

func TestErrorExitContract(t *testing.T) {
	for name, want := range map[string]int{"invalid_input": 2, "not_found": 2, "revision_conflict": 5, "command_conflict": 5, "task_busy": 5, "workspace_busy": 5, "owner_mismatch": 5, "invalid_state": 5, "state_unavailable": 9, "state_corrupt": 9, "schema_newer": 9, "history_too_large": 9} {
		var got *core.Error
		if !errors.As(problem(name, "synthetic"), &got) || got.Exit != want {
			t.Fatalf("%s exit=%+v want%d", name, got, want)
		}
	}
}
