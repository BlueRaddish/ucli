package state

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func waitMigrationMarker(path string) error {
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("synthetic process barrier timed out")
}

func TestMigrationProcessHelper(t *testing.T) {
	mode := os.Getenv("UCLI_MIGRATION_TEST_MODE")
	if mode == "" {
		return
	}
	home, control := os.Getenv("UCLI_MIGRATION_TEST_HOME"), os.Getenv("UCLI_MIGRATION_TEST_CONTROL")
	marker := func(name string) {
		if err := os.WriteFile(filepath.Join(control, name), []byte("synthetic barrier"), 0600); err != nil {
			os.Exit(71)
		}
	}
	wait := func(name string) {
		if waitMigrationMarker(filepath.Join(control, name)) != nil {
			os.Exit(72)
		}
	}
	result := func(code string, receipt MigrationReceipt) {
		json.NewEncoder(os.Stdout).Encode(struct {
			Code    string
			Receipt MigrationReceipt
		}{code, receipt})
	}
	if mode == "v1-hold" || mode == "v1-wait" {
		// Frozen v1 cooperative writer: the old BEGIN IMMEDIATE + literal epoch1
		// fence executes in a different OS process on an already-open connection.
		// It deliberately does not use the new dual-epoch Store implementation.
		db, err := sql.Open("sqlite", backupURI(filepath.Join(home, "state", "state.db"), url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(250)", "synchronous(FULL)", "foreign_keys(1)"}}))
		if err != nil {
			os.Exit(70)
		}
		defer db.Close()
		conn, err := db.Conn(background)
		if err != nil {
			os.Exit(70)
		}
		defer conn.Close()
		begin := func() error { _, err := conn.ExecContext(background, "BEGIN IMMEDIATE"); return err }
		writeV1 := func() string {
			var epoch int
			if err := conn.QueryRowContext(background, "PRAGMA user_version").Scan(&epoch); err != nil {
				return "failed"
			}
			if epoch > 1 {
				_, _ = conn.ExecContext(background, "ROLLBACK")
				return "schema_newer"
			}
			if epoch != 1 {
				_, _ = conn.ExecContext(background, "ROLLBACK")
				return "state_corrupt"
			}
			if _, err := conn.ExecContext(background, `UPDATE tasks SET body=json_set(body,'$.title','old writer committed') WHERE id='task_000'`); err != nil {
				return "failed"
			}
			return "ok"
		}
		if mode == "v1-hold" {
			if err := begin(); err != nil {
				os.Exit(70)
			}
			if writeV1() != "ok" {
				os.Exit(70)
			}
			marker("ready")
			wait("release")
			if _, err := conn.ExecContext(background, "COMMIT"); err != nil {
				os.Exit(70)
			}
			result("ok", MigrationReceipt{})
			os.Exit(0)
		}
		marker("ready")
		wait("start")
		if err := begin(); code(stateError(err)) != "state_busy" {
			result("barrier_not_observed", MigrationReceipt{})
			os.Exit(0)
		}
		marker("blocked")
		wait("retry")
		if err := begin(); err != nil {
			result(code(stateError(err)), MigrationReceipt{})
			os.Exit(0)
		}
		answer := writeV1()
		if answer == "ok" {
			_, _ = conn.ExecContext(background, "COMMIT")
		}
		result(answer, MigrationReceipt{})
		os.Exit(0)
	}
	s, err := Open(home)
	if err != nil {
		result(code(err), MigrationReceipt{})
		os.Exit(0)
	}
	phase := os.Getenv("UCLI_MIGRATION_TEST_PHASE")
	receipt, err := s.migrate(background, os.Getenv("UCLI_MIGRATION_TEST_BACKUP"), func(stage string) error {
		if stage == phase {
			marker("ready")
			if mode == "migrate-crash" {
				os.Exit(73)
			}
			wait("release")
		}
		return nil
	})
	if mode == "migrate-after-commit" && err == nil {
		os.Exit(73)
	}
	result(code(err), receipt)
	_ = s.Close()
	os.Exit(0)
}

type migrationChild struct {
	cmd            *exec.Cmd
	done           chan struct{}
	err            error
	stdout, stderr bytes.Buffer
}

func startMigrationChild(t *testing.T, mode, home, control, backup, phase string) *migrationChild {
	t.Helper()
	child := &migrationChild{done: make(chan struct{})}
	child.cmd = exec.Command(os.Args[0], "-test.run=^TestMigrationProcessHelper$")
	hideTestChild(child.cmd)
	child.cmd.Env = []string{"UCLI_MIGRATION_TEST_MODE=" + mode, "UCLI_MIGRATION_TEST_HOME=" + home, "UCLI_MIGRATION_TEST_CONTROL=" + control, "UCLI_MIGRATION_TEST_BACKUP=" + backup, "UCLI_MIGRATION_TEST_PHASE=" + phase}
	if value := os.Getenv("SystemRoot"); value != "" {
		child.cmd.Env = append(child.cmd.Env, "SystemRoot="+value)
	}
	child.cmd.Stdout, child.cmd.Stderr = &child.stdout, &child.stderr
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { child.err = child.cmd.Wait(); close(child.done) }()
	t.Cleanup(func() {
		select {
		case <-child.done:
		default:
			_ = child.cmd.Process.Kill()
			<-child.done
		}
	})
	return child
}

func (child *migrationChild) wait(t *testing.T, crash bool) (string, MigrationReceipt) {
	t.Helper()
	select {
	case <-child.done:
	case <-time.After(15 * time.Second):
		t.Fatal("migration child timed out")
	}
	if crash {
		var exit *exec.ExitError
		if !errors.As(child.err, &exit) || exit.ExitCode() != 73 {
			t.Fatalf("expected abrupt child exit: %v %s", child.err, child.stderr.String())
		}
		return "", MigrationReceipt{}
	}
	if child.err != nil {
		t.Fatalf("migration child: %v %s", child.err, child.stderr.String())
	}
	var result struct {
		Code    string
		Receipt MigrationReceipt
	}
	if err := json.Unmarshal(child.stdout.Bytes(), &result); err != nil {
		t.Fatalf("child result: %q %v", child.stdout.String(), err)
	}
	return result.Code, result.Receipt
}

func markMigration(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
}
func awaitMigration(t *testing.T, directory, name string) {
	t.Helper()
	if err := waitMigrationMarker(filepath.Join(directory, name)); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationBarrierExcludesAlreadyOpenV1WriterAndThenFencesIt(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	seedSummaryTasks(t, s, summaryFixture(0))
	before := taskBytes(t, s)
	migrateControl, oldControl := t.TempDir(), t.TempDir()
	backup := filepath.Join(t.TempDir(), "before.sqlite")
	migrator := startMigrationChild(t, "migrate-hold", home, migrateControl, backup, "backup")
	awaitMigration(t, migrateControl, "ready")
	old := startMigrationChild(t, "v1-wait", home, oldControl, "", "")
	awaitMigration(t, oldControl, "ready")
	markMigration(t, oldControl, "start")
	awaitMigration(t, oldControl, "blocked") // Actual SQLITE_BUSY under the barrier.
	if !reflect.DeepEqual(before, taskBytes(t, s)) {
		t.Fatal("old writer changed barrier snapshot")
	}
	markMigration(t, migrateControl, "release")
	code, receipt := migrator.wait(t, false)
	if code != "" || receipt.Status != "applied" {
		t.Fatal(code, receipt)
	}
	markMigration(t, oldControl, "retry")
	code, _ = old.wait(t, false)
	if code != "schema_newer" {
		t.Fatal("already-open v1 writer crossed new epoch", code)
	}
	if !reflect.DeepEqual(before, taskBytes(t, s)) {
		t.Fatal("fenced old writer changed migrated task")
	}
	if snapshot := taskBytes(t, openSnapshotTest(t, backup)); !reflect.DeepEqual(before, snapshot) {
		t.Fatal("pre-migration snapshot differs")
	}
}

func TestMigrationWaitsForExistingWriterAndBacksUpItsCommittedState(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	seedSummaryTasks(t, s, summaryFixture(0))
	if _, err := s.db.Exec("PRAGMA busy_timeout=250"); err != nil {
		t.Fatal(err)
	}
	control := t.TempDir()
	old := startMigrationChild(t, "v1-hold", home, control, "", "")
	awaitMigration(t, control, "ready")
	path := filepath.Join(t.TempDir(), "before.sqlite")
	receipt, err := s.Migrate(background, path)
	requireCode(t, err, "state_busy")
	if receipt.Status != "not_applied" || receipt.Backup.Path != "" {
		t.Fatal(receipt)
	}
	markMigration(t, control, "release")
	if code, _ := old.wait(t, false); code != "ok" {
		t.Fatal(code)
	}
	committed := taskBytes(t, s)
	receipt, err = s.Migrate(background, path)
	if err != nil || receipt.Status != "applied" {
		t.Fatal(receipt, err)
	}
	if snapshot := taskBytes(t, openSnapshotTest(t, path)); !reflect.DeepEqual(committed, snapshot) {
		t.Fatal("backup missed previously held writer commit")
	}
}

func TestMigrationProcessCrashBeforeAndAfterCommitRetainsRecoveryEvidence(t *testing.T) {
	for _, mode := range []string{"migrate-crash", "migrate-after-commit"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			s := openTest(t, home)
			seedSummaryTasks(t, s, summaryFixture(0))
			before := taskBytes(t, s)
			backup := filepath.Join(t.TempDir(), "before.sqlite")
			phase := "backfill"
			if mode == "migrate-after-commit" {
				phase = ""
			}
			child := startMigrationChild(t, mode, home, t.TempDir(), backup, phase)
			child.wait(t, true)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, home)
			version, err := s.SchemaVersion(background)
			want := 1
			if mode == "migrate-after-commit" {
				want = 2
			}
			if err != nil || version != want || !reflect.DeepEqual(before, taskBytes(t, s)) {
				t.Fatal("crash recovery", version, want, err)
			}
			if _, err := os.Stat(backup + ".receipt.json"); err != nil {
				t.Fatal("lost pre-migration certificate", err)
			}
			if snapshot := taskBytes(t, openSnapshotTest(t, backup)); !reflect.DeepEqual(before, snapshot) {
				t.Fatal("crash lost pre-migration state")
			}
			if err := s.CheckIntegrity(background); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationRealCapacityFailureRollsBackDDL(t *testing.T) {
	s := openTest(t, t.TempDir())
	seedSummaryTasks(t, s, summaryFixture(0))
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.Migrate(context.Background(), filepath.Join(t.TempDir(), "before.sqlite"))
	if err == nil || receipt.Status != "not_applied" || receipt.Backup.Path == "" {
		t.Fatal("capacity failure lost source/backup distinction", receipt, err)
	}
	version, versionErr := s.SchemaVersion(background)
	if versionErr != nil || version != 1 {
		t.Fatal(version, versionErr)
	}
}
