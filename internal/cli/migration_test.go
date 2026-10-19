package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

func TestMigrationCommandPreservesTasksAndExplicitSchema(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task, err := a.Draft(ctx, "", "Original unsent draft", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if schema, err := a.Store.SchemaVersion(ctx); err != nil || schema != 1 {
		t.Fatalf("open upgraded home: %d %v", schema, err)
	}
	_ = a.Close()
	backup := filepath.Join(t.TempDir(), "before.sqlite")
	input := &unreadBackupInput{}
	var out, diagnostic bytes.Buffer
	code := Run(ctx, []string{"migrate", backup, "--home", home, "--json", "--no-input"}, input, &out, &diagnostic)
	items := jsonLines(t, out.String())
	var receipt state.MigrationReceipt
	if code != 0 || diagnostic.Len() != 0 || input.reads != 0 || len(items) != 1 || items[0].Type != "migration" || json.Unmarshal(items[0].Data, &receipt) != nil {
		t.Fatalf("migration reply: %d %s %s reads%d", code, out.String(), diagnostic.String(), input.reads)
	}
	physical, err := filepath.EvalSymlinks(backup)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "applied" || receipt.FromSchema != 1 || receipt.ToSchema != 2 || receipt.Backup.Path != physical || receipt.Backup.Schema != 1 || receipt.ReceiptPath != physical+".receipt.json" {
		t.Fatalf("inaccurate receipt: %+v", receipt)
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	code, text, progress := call(t, home, "", "migrate", backup, "--json")
	items = jsonLines(t, text)
	if code != 0 || progress != "" || len(items) != 1 || json.Unmarshal(items[0].Data, &receipt) != nil || receipt.Status != "unchanged" || receipt.Backup.Path != "" {
		t.Fatalf("repeat migration: %d %s %s", code, text, progress)
	}
	if after, err := os.ReadFile(backup); err != nil || !bytes.Equal(original, after) {
		t.Fatal("unchanged migration replaced backup", err)
	}
	actual, attempts := taskReceipt(t, home, task.ID)
	if actual.Draft != task.Draft || actual.Revision != task.Revision || len(attempts) != 0 {
		t.Fatalf("migration changed work: %+v %+v", actual, attempts)
	}
	code, text, progress = call(t, home, "", "doctor", "--json")
	var doctor struct {
		Schema    int    `json:"state_schema"`
		Integrity string `json:"integrity"`
	}
	items = jsonLines(t, text)
	if code != 0 || progress != "" || len(items) != 1 || json.Unmarshal(items[0].Data, &doctor) != nil || doctor.Schema != 2 || doctor.Integrity != "ok" {
		t.Fatalf("doctor: %d %s %s", code, text, progress)
	}
}

func TestMigrationCommandFailureReturnsOutcomeAndNeverClobbers(t *testing.T) {
	home, destination := t.TempDir(), filepath.Join(t.TempDir(), "occupied.sqlite")
	if err := os.WriteFile(destination, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	code, text, _ := call(t, home, "", "migrate", destination, "--json")
	items := jsonLines(t, text)
	var receipt state.MigrationReceipt
	if code == 0 || len(items) != 2 || items[0].Type != "migration" || items[1].Type != "error" || json.Unmarshal(items[0].Data, &receipt) != nil || receipt.Status != "not_applied" {
		t.Fatalf("lost failure outcome: %d %s", code, text)
	}
	if raw, _ := os.ReadFile(destination); string(raw) != "keep me" {
		t.Fatal("migration clobbered destination")
	}
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if schema, err := a.Store.SchemaVersion(context.Background()); err != nil || schema != 1 {
		t.Fatalf("failed migration advanced state: %d %v", schema, err)
	}
}
