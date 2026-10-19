package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

func openSnapshotTest(t *testing.T, path string) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", backupURI(path, url.Values{"mode": {"ro"}, "immutable": {"1"}}))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return &Store{db: db}
}

func TestBackupIncludesWALIntentEvidenceAndUnknownFence(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	if _, err := s.db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	cmd := command("snapshot-command", "snapshot-workspace", "read-only")
	a := accept(t, s, cmd)
	if _, err := s.AppendEvent(background, core.Event{AttemptID: a.ID, Type: "warning", Text: "Synthetic retained warning"}, a.Owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishWithCause(background, a.ID, a.Owner.ID, "unknown", "Unsettled output", "External stop is unconfirmed.", "deadline"); err != nil {
		t.Fatal(err)
	}
	task, err := s.GetTask(background, a.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Draft = "Saved follow-up remains separate"
	task, err = s.SaveDraft(background, task, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	wantAttempt, _ := s.GetAttempt(background, a.ID)
	wantEvents, _ := s.Events(background, a.ID, 0, 100)
	if stat, err := os.Stat(filepath.Join(home, "state", "state.db-wal")); err != nil || stat.Size() == 0 {
		t.Fatalf("test needs real uncheckpointed WAL: %v %v", stat, err)
	}
	destination := filepath.Join(t.TempDir(), "backup #%.sqlite")
	receipt, err := s.Backup(background, destination)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Path != destination || receipt.Version != 1 || receipt.Schema != 1 || receipt.Tasks != 1 || receipt.Attempts != 1 || receipt.Events != int64(len(wantEvents)) || receipt.UnsettledAttempts != 1 || receipt.CreatedAt.IsZero() || len(receipt.Warnings) != 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if receipt.SHA256 != hex.EncodeToString(hash[:]) || receipt.Bytes != int64(len(raw)) {
		t.Fatal("backup hash/size does not identify published bytes")
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(destination)
		if statErr != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("snapshot exposes task data: %v %v", info, statErr)
		}
	}
	copy := openSnapshotTest(t, destination)
	if err = copy.CheckIntegrity(background); err != nil {
		t.Fatal(err)
	}
	gotTask, err := copy.GetTask(background, task.ID)
	if err != nil || !reflect.DeepEqual(gotTask, task) {
		t.Fatalf("snapshot lost WAL draft: %+v %v", gotTask, err)
	}
	gotAttempt, err := copy.GetAttempt(background, a.ID)
	if err != nil || !reflect.DeepEqual(gotAttempt, wantAttempt) {
		t.Fatalf("snapshot lost unsettled receipt: %+v %v", gotAttempt, err)
	}
	gotEvents, err := copy.Events(background, a.ID, 0, 100)
	if err != nil || !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Fatalf("snapshot event evidence differs: %+v %v", gotEvents, err)
	}
	var leases int
	if err = copy.db.QueryRow("SELECT count(*) FROM leases").Scan(&leases); err != nil || leases != 1 {
		t.Fatalf("snapshot released unknown workspace fence: %d %v", leases, err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err = os.Stat(destination + suffix); !os.IsNotExist(err) {
			t.Fatalf("snapshot needs unexpected companion %s: %v", suffix, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(destination))
	if len(entries) != 1 {
		t.Fatalf("staging leaked: %v", entries)
	}
}

func TestBackupDoesNotPublishDamagedSource(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("snapshot-damage", "workspace", "read-only"))
	for _, statement := range []string{"PRAGMA ignore_check_constraints=ON", "UPDATE tasks SET revision=0", "PRAGMA ignore_check_constraints=OFF"} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	requireCode(t, s.CheckIntegrity(background), "state_corrupt")
	parent := t.TempDir()
	destination := filepath.Join(parent, "must-not-be-certified.db")
	receipt, err := s.Backup(background, destination)
	requireCode(t, err, "state_corrupt")
	if receipt.Path != "" {
		t.Fatal("damaged backup returned a successful receipt")
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed backup left publication or staging: %v %v", entries, readErr)
	}
	var revision int
	if err = s.db.QueryRow("SELECT revision FROM tasks WHERE id=?", a.TaskID).Scan(&revision); err != nil || revision != 0 {
		t.Fatalf("backup changed damaged source: %d %v", revision, err)
	}
}

type cancelingBackupReader struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancelingBackupReader) Read(p []byte) (int, error) {
	r.reads++
	p[0] = 'a'
	r.cancel()
	return 1, nil
}

func TestBackupHashReaderHonorsCancellationBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	source := &cancelingBackupReader{cancel: cancel}
	reader := backupReader{ctx: ctx, Reader: source}
	buf := make([]byte, 64*1024)
	if n, err := reader.Read(buf); n != 1 || err != nil {
		t.Fatalf("first chunk=%d %v", n, err)
	}
	if n, err := reader.Read(buf); n != 0 || !errors.Is(err, context.Canceled) || source.reads != 1 {
		t.Fatalf("canceled hashing kept reading: n=%d reads=%d err=%v", n, source.reads, err)
	}
}

func TestBackupPinnedSnapshotAllowsConcurrentWriter(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	other := openTest(t, home)
	cmd := command("snapshot-writer", "snapshot-workspace", "read-only")
	task, err := s.SaveDraft(background, core.Task{Workspace: cmd.Workspace, Selection: cmd.Requested, Draft: strings.Repeat("a", core.MaxInputBytes)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "pinned.db")
	if err = os.WriteFile(destination, nil, 0600); err != nil {
		t.Fatal(err)
	}
	steps := 0
	err = s.read(background, func(conn executor) error {
		return copySnapshot(background, conn.(*sql.Conn), destination, func() error {
			steps++
			if steps != 1 {
				return nil
			}
			newer := task
			newer.Draft = "Concurrent new draft"
			_, err := other.SaveDraft(background, newer, newer.Revision)
			return err
		})
	})
	if err != nil || steps < 2 {
		t.Fatalf("real stepped snapshot: steps=%d err=%v", steps, err)
	}
	copy := openSnapshotTest(t, destination)
	got, err := copy.GetTask(background, task.ID)
	if err != nil || !reflect.DeepEqual(got, task) {
		t.Fatalf("backup mixed a concurrent writer into pinned snapshot: %v %v", got.Revision, err)
	}
	newer, err := s.GetTask(background, task.ID)
	if err != nil || newer.Revision != task.Revision+1 || newer.Draft != "Concurrent new draft" {
		t.Fatalf("source writer failed: %+v %v", newer, err)
	}
}

func TestBackupRefusesAliasesExistingPathsAndNewEpoch(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	stateDir := filepath.Join(home, "state")
	if err := os.Mkdir(filepath.Join(stateDir, "subdirectory"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"relative.sqlite", "file:///tmp/backup.db", filepath.Join(stateDir, "new.db"), filepath.Join(stateDir, "state.db-wal"), filepath.Join(stateDir, "subdirectory", "snapshot.db")} {
		if receipt, err := s.Backup(background, dest); err == nil || receipt.Path != "" {
			t.Fatalf("unsafe target accepted: %q %+v %v", dest, receipt, err)
		}
	}
	existing := filepath.Join(t.TempDir(), "foreign.sqlite")
	if err := os.WriteFile(existing, []byte("foreign bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Backup(background, existing)
	requireCode(t, err, "backup_exists")
	if raw, _ := os.ReadFile(existing); string(raw) != "foreign bytes" {
		t.Fatal("backup overwrote existing path")
	}
	alias := filepath.Join(t.TempDir(), "state-alias")
	if err := os.Symlink(stateDir, alias); err == nil {
		_, err = s.Backup(background, filepath.Join(alias, "new.db"))
		requireCode(t, err, "invalid_input")
	} else {
		t.Logf("symlink-specific target guard not exercised: %v", err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(t.TempDir(), "newer.db")
	_, err = s.Backup(background, newer)
	requireCode(t, err, "schema_newer")
	if _, err = os.Stat(newer); !os.IsNotExist(err) {
		t.Fatalf("new-epoch refusal published a snapshot: %v", err)
	}
}

func TestBackupCancellationFinalizesAndPreservesSource(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx, cancel := context.WithCancel(background)
	cancel()
	destination := filepath.Join(t.TempDir(), "canceled.db")
	if _, err := s.Backup(ctx, destination); !errors.Is(err, context.Canceled) {
		t.Fatal("snapshot lost cancellation identity", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("canceled backup published: %v", err)
	}
	cmd := command("copy-cancel", "snapshot-workspace", "read-only")
	if _, err := s.SaveDraft(background, core.Task{Workspace: cmd.Workspace, Selection: cmd.Requested, Draft: strings.Repeat("b", core.MaxInputBytes)}, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(background)
	defer cancel()
	if err := os.WriteFile(destination, nil, 0600); err != nil {
		t.Fatal(err)
	}
	steps := 0
	err := s.transaction(ctx, false, true, func(conn executor) error {
		return copySnapshot(ctx, conn.(*sql.Conn), destination, func() error {
			steps++
			cancel()
			return nil
		})
	})
	if !errors.Is(err, context.Canceled) || steps != 1 {
		t.Fatalf("mid-copy cancellation: steps=%d err=%v", steps, err)
	}
	// Opening the incomplete private file for an exclusive write proves the
	// backup destination was finalized/closed; it was never a published backup.
	db, err := sql.Open("sqlite", backupURI(destination, url.Values{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"BEGIN EXCLUSIVE", "ROLLBACK"} {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckIntegrity(background); err != nil {
		t.Fatalf("cancellation damaged source: %v", err)
	}
}
