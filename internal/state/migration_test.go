package state

import (
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
)

func migrateTest(t *testing.T, s *Store) MigrationReceipt {
	t.Helper()
	receipt, err := s.Migrate(background, filepath.Join(t.TempDir(), "before.sqlite"))
	if err != nil || receipt.Status != "applied" {
		t.Fatalf("migration=%+v err=%v", receipt, err)
	}
	return receipt
}

func taskBytes(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query("SELECT id,updated_at,body FROM tasks ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id, at, body string
		if err := rows.Scan(&id, &at, &body); err != nil {
			t.Fatal(err)
		}
		result = append(result, id, at, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMigrationExplicitVerifiedSnapshotAndUntouchedOriginalBodies(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	version, err := s.SchemaVersion(background)
	if err != nil || version != 1 {
		t.Fatal("new homes must remain schema1", version, err)
	}
	seedSummaryTasks(t, s, summaryFixture(0), summaryFixture(1), summaryFixture(2))
	if _, err = s.db.Exec(`UPDATE tasks SET body=json_set(body,'$.future',json('{"nested":[1,"opaque"]}')),updated_at='2026-10-18T01:00:00Z' WHERE id='task_001'`); err != nil {
		t.Fatal(err)
	}
	before := taskBytes(t, s)
	priorPage, err := s.ListTaskSummaries(background, "Task", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	receipt := migrateTest(t, s)
	if receipt.FromSchema != 1 || receipt.ToSchema != 2 || receipt.Backup.Schema != 1 || receipt.ReceiptPath == "" || receipt.Backup.Tasks != 3 {
		t.Fatalf("receipt=%+v", receipt)
	}
	if after := taskBytes(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("migration rewrote original JSON or timestamp spelling")
	}
	backup := openSnapshotTest(t, receipt.Backup.Path)
	if snapshot := taskBytes(t, backup); !reflect.DeepEqual(before, snapshot) {
		t.Fatal("backup differs from pre-migration task state")
	}
	var certificate migrationCertificate
	raw, err := os.ReadFile(receipt.ReceiptPath)
	if err != nil || json.Unmarshal(raw, &certificate) != nil || certificate.Backup.SHA256 != receipt.Backup.SHA256 || strings.Contains(string(raw), `"status"`) {
		t.Fatal("invalid pre-migration certificate", err)
	}
	snapshot, err := os.ReadFile(receipt.Backup.Path)
	hash := sha256.Sum256(snapshot)
	if err != nil || hex.EncodeToString(hash[:]) != receipt.Backup.SHA256 {
		t.Fatal("snapshot certificate hash mismatch", err)
	}
	if err = s.CheckIntegrity(background); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	page, err := s.ListTaskSummaries(background, "Task", "", 1)
	if err != nil || !reflect.DeepEqual(page, priorPage) {
		t.Fatal("migration changed page/cursor", page, priorPage, err)
	}
	older, err := s.ListTaskSummaries(background, "Task", priorPage.NextCursor, 20)
	if err != nil || len(older.Tasks) != 2 {
		t.Fatal("legacy cursor unusable", older, err)
	}
	unchanged, err := s.Migrate(background, "ignored-already-migrated")
	if err != nil || unchanged.Status != "unchanged" || unchanged.Backup.Path != "" {
		t.Fatal(unchanged, err)
	}
	newBackup, err := s.Backup(background, filepath.Join(t.TempDir(), "epoch2.sqlite"))
	if err != nil || newBackup.Schema != 2 {
		t.Fatal("schema2 backup", newBackup, err)
	}
}

func TestMigrationRefusesUnsettledWorkAndAnyLease(t *testing.T) {
	for _, state := range []string{"running", "unknown", "orphan-lease"} {
		t.Run(state, func(t *testing.T) {
			s := openTest(t, t.TempDir())
			a := accept(t, s, command("migration-owner", "migration-workspace", "read-only"))
			if state == "unknown" {
				if _, err := s.Finish(background, a.ID, a.Owner.ID, "unknown", "", "uncertain"); err != nil {
					t.Fatal(err)
				}
			}
			if state == "orphan-lease" {
				if _, err := s.db.Exec("UPDATE attempts SET state='complete'"); err != nil {
					t.Fatal(err)
				}
			}
			parent := t.TempDir()
			receipt, err := s.Migrate(background, filepath.Join(parent, "blocked.sqlite"))
			requireCode(t, err, "migration_busy")
			entries, _ := os.ReadDir(parent)
			version, _ := s.SchemaVersion(background)
			if receipt.Status != "not_applied" || receipt.Backup.Path != "" || version != 1 || len(entries) != 0 {
				t.Fatal("blocked migration changed state", receipt, entries, version)
			}
		})
	}
}

func TestMigrationFailureRetainsBackupRollsBackAndNeverOverwrites(t *testing.T) {
	for _, phase := range []string{"backup", "backfill", "commit"} {
		t.Run(phase, func(t *testing.T) {
			s := openTest(t, t.TempDir())
			seedSummaryTasks(t, s, summaryFixture(0))
			before := taskBytes(t, s)
			path := filepath.Join(t.TempDir(), "before.sqlite")
			receipt, err := s.migrate(background, path, func(stage string) error {
				if stage == phase {
					return errors.New("synthetic rollback")
				}
				return nil
			})
			if err == nil || receipt.Status != "not_applied" || receipt.Backup.Path == "" || receipt.ReceiptPath == "" {
				t.Fatal(receipt, err)
			}
			version, _ := s.SchemaVersion(background)
			if version != 1 || !reflect.DeepEqual(before, taskBytes(t, s)) {
				t.Fatal("rollback changed original state", version)
			}
			var objects int
			if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'task_metadata%'").Scan(&objects); err != nil || objects != 0 {
				t.Fatal("transactional DDL escaped rollback", objects, err)
			}
			_, err = s.Migrate(background, path)
			requireCode(t, err, "backup_exists")
			if _, err := os.Stat(receipt.ReceiptPath); err != nil {
				t.Fatal("certificate removed on failure", err)
			}
		})
	}
	s := openTest(t, t.TempDir())
	path := filepath.Join(t.TempDir(), "before.sqlite")
	if err := os.WriteFile(path+".receipt.json", []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Migrate(background, path)
	requireCode(t, err, "backup_exists")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created backup despite occupied certificate path", err)
	}
}

func TestMigrationCancellationBeforeCommitIsConservativelyUnknown(t *testing.T) {
	s := openTest(t, t.TempDir())
	seedSummaryTasks(t, s, summaryFixture(0))
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	receipt, err := s.migrate(ctx, filepath.Join(t.TempDir(), "before.sqlite"), func(stage string) error {
		if stage == "commit" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || receipt.Status != "unknown" || receipt.Backup.Path == "" {
		t.Fatal(receipt, err)
	}
	version, err := s.SchemaVersion(background)
	if err != nil || version != 1 {
		t.Fatal("fixture cancellation should roll back, independently inspected", version, err)
	}
}
