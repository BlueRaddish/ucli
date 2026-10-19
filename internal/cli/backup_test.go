package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

type unreadBackupInput struct{ reads int }

func (r *unreadBackupInput) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("backup must never read stdin")
}

func TestBackupSnapshotIncludesLiveWALAndReopensForInspectionAndReplay(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close() // Keep the writer open so committed state remains in its WAL.
	ctx := context.Background()
	input := "Completed task for snapshot"
	complete, err := a.Run(ctx, app.Options{Input: input, CWD: cwd, CommandID: "backup-complete", Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := a.Draft(ctx, complete.TaskID, "Unsent follow-up in snapshot", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	owner := core.Owner{ID: core.NewID("owner"), PID: os.Getpid(), StartedAt: time.Now().UTC()}
	uncertain, _, err := a.Store.Accept(ctx, core.RunCommand{ID: "backup-uncertain", Input: "Unsettled snapshot fixture", Requested: core.Defaults(), Effective: complete.Effective, Workspace: complete.Workspace, Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	uncertain, err = a.Store.Finish(ctx, uncertain.ID, owner.ID, "unknown", "Partial saved output", "External outcome remains uncertain")
	if err != nil {
		t.Fatal(err)
	}
	completeEvents, err := a.Store.Events(ctx, complete.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	uncertainEvents, err := a.Store.Events(ctx, uncertain.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(home, "state", "state.db-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("fixture needs a real nonempty WAL: %v %v", info, err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot #%.sqlite")
	// Keep the relative-path fixture on one volume: hosted Windows checkouts
	// and temporary directories can live on different drives.
	t.Chdir(filepath.Dir(destination))
	var out, diagnostic bytes.Buffer
	stdin := &unreadBackupInput{}
	code := Run(ctx, []string{"backup", filepath.Base(destination), "--home", home, "--json", "--no-input"}, stdin, &out, &diagnostic)
	items := jsonLines(t, out.String())
	var receipt state.BackupReceipt
	if code != 0 || diagnostic.Len() != 0 || stdin.reads != 0 || len(items) != 1 || items[0].Type != "backup" || json.Unmarshal(items[0].Data, &receipt) != nil {
		t.Fatalf("backup reply: %d %s %s stdin reads%d", code, out.String(), diagnostic.String(), stdin.reads)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	physical, err := filepath.EvalSymlinks(destination)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Version != 1 || receipt.Schema != 1 || receipt.Path != physical || receipt.SHA256 != hex.EncodeToString(hash[:]) || receipt.Bytes != int64(len(raw)) || receipt.CreatedAt.IsZero() || receipt.Tasks != 2 || receipt.Attempts != 2 || receipt.Events != int64(len(completeEvents)+len(uncertainEvents)) || receipt.UnsettledAttempts != 1 {
		t.Fatalf("receipt does not identify the snapshot: %+v", receipt)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(destination + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("backup unexpectedly needs %s: %v", suffix, err)
		}
	}
	// Test-only reconstruction of an isolated home consumes the actual snapshot.
	// UCLI deliberately has no restore command or automatic replay behavior.
	copyHome := t.TempDir()
	copyDir := filepath.Join(copyHome, "state")
	if err := os.Mkdir(copyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "state.db"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	copiedTask, attempts := taskReceipt(t, copyHome, complete.TaskID)
	if !reflect.DeepEqual(copiedTask, draft) || len(attempts) != 1 || !reflect.DeepEqual(attempts[0], complete) {
		t.Fatalf("snapshot lost saved draft/result: %+v %+v", copiedTask, attempts)
	}
	code, text, progress := call(t, copyHome, "", "run", "--demo", "--json", "--cwd", cwd, "--command-id", "backup-complete", input)
	if code != 0 || progress != "" || attemptReceipt(t, text).ID != complete.ID || !strings.Contains(text, `"type":"command_replayed"`) {
		t.Fatalf("snapshot cannot replay a durable receipt: %d %s %s", code, text, progress)
	}
	code, text, progress = call(t, copyHome, "", "run", "--demo", "--json", "--cwd", cwd, "--command-id", "backup-uncertain", uncertain.Input)
	if code != 7 || progress != "" || attemptReceipt(t, text).ID != uncertain.ID || attemptReceipt(t, text).State != "unknown" {
		t.Fatalf("snapshot replay settled or duplicated uncertainty: %d %s %s", code, text, progress)
	}
	copyApp, err := app.Open(copyHome)
	if err != nil {
		t.Fatal(err)
	}
	defer copyApp.Close()
	for id, want := range map[string][]core.Event{complete.ID: completeEvents, uncertain.ID: uncertainEvents} {
		got, err := copyApp.Store.Events(ctx, id, 0, 100)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot/replay changed saved events: %+v %v", got, err)
		}
	}
	_, attempts = taskReceipt(t, copyHome, uncertain.TaskID)
	if len(attempts) != 1 || !reflect.DeepEqual(attempts[0], uncertain) {
		t.Fatal("unsettled attempt changed after opening snapshot")
	}
	humanPath := filepath.Join(t.TempDir(), "human.sqlite")
	code, text, progress = call(t, home, "", "backup", humanPath)
	if code != 0 || progress != "" {
		t.Fatalf("human backup: %d %s %s", code, text, progress)
	}
	humanPhysical, err := filepath.EvalSymlinks(humanPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Backup saved: " + humanPhysical, "SHA-256:", "Tasks: 2 | attempts: 2", "unsettled attempts: 1", "Native credential stores are not included", "Unsettled attempts remain unsettled", "No work was restored or resumed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing backup feedback %q: %s", want, text)
		}
	}
}

func TestBackupRefusalsCancellationAndDiscoveryNeverClaimSuccess(t *testing.T) {
	home := t.TempDir()
	destination := filepath.Join(t.TempDir(), "keep.sqlite")
	if err := os.WriteFile(destination, []byte("preserve existing destination"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		exit int
	}{
		{destination, 5},
		{filepath.Join(home, "state", "backup.sqlite"), 2},
		{filepath.Join(t.TempDir(), "missing", "backup.sqlite"), 2},
	} {
		code, out, diagnostic := call(t, home, "", "backup", tc.path, "--json")
		items := jsonLines(t, out)
		if code != tc.exit || diagnostic != "" || len(items) != 1 || items[0].Type != "error" || strings.Contains(out, `"type":"backup"`) {
			t.Fatalf("refusal claimed backup: %d %s %s", code, out, diagnostic)
		}
	}
	if raw, err := os.ReadFile(destination); err != nil || string(raw) != "preserve existing destination" {
		t.Fatal("existing destination changed", err)
	}
	for _, timeout := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := 130
		if timeout {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = 124
		} else {
			cancel()
		}
		path := filepath.Join(t.TempDir(), "canceled.sqlite")
		var out, diagnostic bytes.Buffer
		code := Run(ctx, []string{"--home", home, "backup", path, "--json"}, &unreadBackupInput{}, &out, &diagnostic)
		cancel()
		items := jsonLines(t, out.String())
		if code != want || diagnostic.Len() != 0 || len(items) != 1 || items[0].Type != "error" || !strings.Contains(out.String(), "Check the destination") || strings.Contains(out.String(), "inspect ucli tasks") {
			t.Fatalf("cancellation claimed success: %d %s %s", code, out.String(), diagnostic.String())
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled command published a backup: %v", err)
		}
	}
	missing := filepath.Join(t.TempDir(), "not-opened")
	for _, args := range [][]string{
		{"backup"}, {"backup", "-"}, {"backup", ""}, {"backup", "a", "prompt"},
		{"backup", "a", "--demo"}, {"backup", "a", "--model", "auto"},
		{"backup", "a", "--account", "auto"}, {"backup", "a", "--effort", "auto"},
		{"backup", "a", "--permission", "read-only"}, {"backup", "a", "--cwd", home},
		{"backup", "a", "--command-id", "x"}, {"backup", "a", "--search", "x"},
		{"backup", "a", "--limit", "2"}, {"backup", "a", "--before-revision", "0"},
	} {
		if code, out, diagnostic := call(t, missing, "never read this", args...); code != 2 || strings.Contains(out+diagnostic, "Backup saved:") {
			t.Fatalf("invalid backup invocation %v: %d %s %s", args, code, out, diagnostic)
		}
	}
	code, out, diagnostic := call(t, missing, "", "backup", "--help")
	if code != 0 || diagnostic != "" || !strings.Contains(out, "existing files are never overwritten") || strings.Contains(out, "--model") || strings.Contains(out, "reads redirected stdin") {
		t.Fatalf("backup help: %d %s %s", code, out, diagnostic)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid command or help opened state: %v", err)
	}
}
