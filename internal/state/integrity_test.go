package state

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExplicitIntegrityAuditHealthyReadOnlyAndUnavailable(t *testing.T) {
	s := openTest(t, t.TempDir())
	if err := s.CheckIntegrity(background); err != nil {
		t.Fatal("empty database failed audit", err)
	}
	a := accept(t, s, command("integrity-healthy", "workspace", "read-only"))
	if _, err := s.Finish(background, a.ID, a.Owner.ID, "complete", "synthetic evidence", "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckIntegrity(background); err != nil {
		t.Fatal("populated read-only database failed audit", err)
	}
	ctx, cancel := context.WithCancel(background)
	cancel()
	if err := s.CheckIntegrity(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("audit lost cancellation identity", err)
	}
	if err := s.CheckIntegrity(background); err != nil {
		t.Fatal("canceled audit poisoned subsequent read", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.CheckIntegrity(background), "state_unavailable")
}

func TestExplicitIntegrityAuditRejectsUnreadConstraintDamage(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	a := accept(t, s, command("integrity-damage", "workspace", "read-only"))
	if err := s.CheckIntegrity(background); err != nil {
		t.Fatal("control failed before intentional damage", err)
	}
	// Test-only engine corruption: keep a valid header/schema/index layout but
	// violate a real CHECK constraint in a row normal startup never reads.
	if _, err := s.db.Exec("PRAGMA ignore_check_constraints=ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE tasks SET revision=0 WHERE id=?", a.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA ignore_check_constraints=OFF"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home) // Opening is deliberately not a whole-database audit.
	err := s.CheckIntegrity(background)
	requireCode(t, err, "state_corrupt")
	if strings.Contains(err.Error(), a.TaskID) || strings.Contains(err.Error(), "CHECK") {
		t.Fatal("SQLite record diagnostics escaped the audit", err)
	}
	var revision int
	if err := s.db.QueryRow("SELECT revision FROM tasks WHERE id=?", a.TaskID).Scan(&revision); err != nil || revision != 0 {
		t.Fatal("audit changed damaged evidence", revision, err)
	}
}

func TestExplicitIntegrityAuditFencesChangedSchema(t *testing.T) {
	s := openTest(t, t.TempDir())
	if _, err := s.db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.CheckIntegrity(background), "schema_newer")
	if _, err := s.db.Exec("PRAGMA user_version=0"); err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.CheckIntegrity(background), "state_corrupt")
}
