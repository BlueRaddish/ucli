package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func TestDoctorAuditsIntegrityInsteadOfTreatingOpenAsHealthy(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Draft(context.Background(), "", "Synthetic integrity fixture", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	code, text, diagnostic := call(t, home, "", "doctor", "--json")
	if code != 0 || diagnostic != "" {
		t.Fatalf("healthy doctor: %d %s %s", code, text, diagnostic)
	}
	var report struct {
		State     string `json:"local_state"`
		Integrity string `json:"integrity"`
	}
	if err := json.Unmarshal(jsonLines(t, text)[0].Data, &report); err != nil || report.State != "opened" || report.Integrity != "ok" {
		t.Fatalf("missing completed integrity report: %s %v", text, err)
	}
	code, text, diagnostic = call(t, home, "", "doctor")
	if code != 0 || diagnostic != "" || !strings.Contains(text, "Full integrity check: passed") {
		t.Fatalf("healthy human doctor: %d %s %s", code, text, diagnostic)
	}
	// Deliberately damage only this temporary database through the installed
	// SQLite driver. Its header/schema stay valid while a real CHECK is violated.
	db, err := sql.Open("sqlite", filepath.Join(home, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, query := range []string{"PRAGMA ignore_check_constraints=ON", "UPDATE tasks SET revision=0", "PRAGMA ignore_check_constraints=OFF"} {
		if _, err := db.Exec(query); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// A bounded catalog query still opens normally; it makes no archive-health
	// claim. This proves doctor is what actually performs the full audit.
	code, text, diagnostic = call(t, home, "", "models", "--json")
	if code != 0 || diagnostic != "" || jsonLines(t, text)[0].Type != "models" {
		t.Fatalf("ordinary open unexpectedly performs the full scan: %d %s %s", code, text, diagnostic)
	}
	code, text, diagnostic = call(t, home, "", "doctor", "--json")
	if code != 9 || diagnostic != "" {
		t.Fatalf("damaged state claimed healthy: %d %s %s", code, text, diagnostic)
	}
	rows := jsonLines(t, text)
	var problem core.Error
	if len(rows) != 1 || rows[0].Type != "error" || json.Unmarshal(rows[0].Data, &problem) != nil || problem.Code != "state_corrupt" || strings.Contains(text, `"integrity":"ok"`) || strings.Contains(text, task.ID) {
		t.Fatalf("wrong or unsanitized integrity failure: %s", text)
	}
	code, text, diagnostic = call(t, home, "", "doctor")
	if code != 9 || text != "" || !strings.Contains(diagnostic, "integrity check") || strings.Contains(diagnostic, "passed") {
		t.Fatalf("human integrity failure: %d %s %s", code, text, diagnostic)
	}
}
