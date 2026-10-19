package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptedFailurePersists(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"marker":"synthetic-p01"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := openService(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.Execute(context.Background(), Request{ID: "closed-reader", TimeoutMS: 1000}, "foreground", func(Event) error {
		return errors.New("reader closed")
	})
	var status string
	if err := s.db.QueryRow("SELECT status FROM attempts WHERE id='closed-reader'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if r.Status != "failed" || r.Error != "reader closed" || status != "failed" {
		t.Fatalf("failed acceptance delivery not recorded: %+v %s", r, status)
	}
}

func TestSQLiteFixGate(t *testing.T) {
	for _, v := range []string{"3.51.3", "3.52.0", "3.50.7", "3.44.6"} {
		if !fixedSQLite(v) {
			t.Fatal("rejected fixed", v)
		}
	}
	for _, v := range []string{"3.51.2", "3.50.6", "3.44.5", "invalid"} {
		if fixedSQLite(v) {
			t.Fatal("accepted vulnerable", v)
		}
	}
}
func TestFramingBoundsAndSplit(t *testing.T) {
	var text string
	w := &frameWriter{emit: func(e Event) error { text = e.Text; return nil }}
	for _, b := range []byte("{\"type\":\"message_delta\",\"text\":\"한글 👋\"}\n") {
		if _, err := w.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if text != "한글 👋" {
		t.Fatal(text)
	}
	if _, err := w.Write([]byte(strings.Repeat("x", maxFrame+1))); err == nil {
		t.Fatal("oversized frame accepted")
	}
}
func TestWorkspaceSubdirectory(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0700)
	sub := filepath.Join(root, "src")
	os.Mkdir(sub, 0700)
	a, err := workspaceIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := workspaceIdentity(sub)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || a.CWD == b.CWD {
		t.Fatalf("identity/cwd mismatch: %+v %+v", a, b)
	}
}
