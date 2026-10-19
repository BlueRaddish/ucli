package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
	"ucli.local/ucli/internal/workspace"
)

const timingMarker = ".ucli-timing-fixture.json"
const timingMaxBytes int64 = 500 * 1024 * 1024
const timingMinFree uint64 = 1024 * 1024 * 1024

type timingManifest struct {
	Version        int    `json:"version"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Size           string `json:"size"`
	Home           string `json:"home"`
	TaskID         string `json:"history_task_id"`
	Tasks          int    `json:"tasks"`
	DraftTasks     int    `json:"draft_tasks"`
	DraftBytes     int    `json:"draft_bytes"`
	Attempts       int    `json:"history_attempts"`
	OutputBytes    int    `json:"output_bytes_per_attempt"`
	OutputTotal    int64  `json:"total_output_bytes"`
	DatabaseBytes  int64  `json:"database_bytes"`
	DatabaseSHA256 string `json:"database_sha256"`
	FilesBytes     int64  `json:"fixture_files_bytes"`
	MaxBytes       int64  `json:"fixture_max_bytes"`
	ObservedFree   uint64 `json:"observed_free_bytes_before_seed"`
	SeededAt       string `json:"seeded_at"`
}

// This route exists only in the test executable. tools/bench_app.py checks live
// free space immediately before invoking it. No production flag accepts seeds,
// no SQL is edited directly, and partial fixtures cannot be benchmarked.
func TestTimingFixtureProcess(t *testing.T) {
	size := os.Getenv("UCLI_TIMING_SEED")
	if size == "" {
		t.Skip("opt-in synthetic timing fixture; use tools/bench_app.py --seed-helper")
	}
	free, err := strconv.ParseUint(os.Getenv("UCLI_TIMING_FREE_BYTES"), 10, 64)
	if err != nil {
		t.Fatal("missing free-space observation from timing runner")
	}
	m, err := seedTimingFixture(os.Getenv("UCLI_TIMING_HOME"), size, free)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(m); err != nil {
		t.Fatal(err)
	}
}

func seedTimingFixture(home, size string, free uint64) (timingManifest, error) {
	m := timingManifest{Version: 1, Kind: "ucli-synthetic-timing", Status: "seeding", Size: size,
		Home: filepath.Clean(home), DraftBytes: 256, MaxBytes: timingMaxBytes, ObservedFree: free}
	switch size {
	case "small":
		m.Tasks, m.Attempts, m.OutputBytes = 10, 2, 4096
	case "large":
		m.Tasks, m.Attempts, m.OutputBytes = 10000, 40, core.MaxOutputBytes
	default:
		return m, fmt.Errorf("fixture size must be small or large")
	}
	if !filepath.IsAbs(home) || free < timingMinFree {
		return m, fmt.Errorf("fixture requires an absolute new home and at least 1 GiB observed free space")
	}
	// Mkdir (not MkdirAll) fails on every existing home, even an empty one.
	// Never delete an old/partial fixture or reuse a user-selected state store.
	if err := os.Mkdir(home, 0700); err != nil {
		return m, fmt.Errorf("fixture requires a nonexistent home: %w", err)
	}
	writeMarker := func() error {
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(home, timingMarker), append(b, '\n'), 0600)
	}
	if err := writeMarker(); err != nil {
		return m, err
	}
	cwd := filepath.Join(home, "workspace")
	if err := os.Mkdir(cwd, 0700); err != nil {
		return m, err
	}
	w, err := workspace.Resolve(cwd)
	if err != nil {
		return m, err
	}
	s, err := state.Open(home)
	if err != nil {
		return m, err
	}
	closed := false
	defer func() {
		if !closed {
			s.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var selected core.Task
	for i := 0; i < m.Tasks; i++ {
		task, err := s.SaveDraft(ctx, core.Task{Title: fmt.Sprintf("Synthetic timing task %05d", i),
			Workspace: w, Draft: strings.Repeat("D", m.DraftBytes), Selection: core.Defaults()}, 0)
		if err != nil {
			return m, err
		}
		if i == 0 {
			selected = task
		}
	}
	owner := core.Owner{ID: "timing-fixture-owner", PID: os.Getpid(), StartedAt: time.Now().UTC()}
	route := core.Route{Runtime: "demo", Provider: "local", Model: "demo/fast", Account: "demo", Effort: "medium", Permission: "read-only", Mode: "demo"}
	output := strings.Repeat("R", m.OutputBytes)
	for i := 0; i < m.Attempts; i++ {
		attempt, fresh, err := s.Accept(ctx, core.RunCommand{ID: fmt.Sprintf("timing-command-%03d", i),
			TaskID: selected.ID, ExpectedRevision: selected.Revision, Input: "Synthetic complete receipt for startup timing; no runtime called.",
			Requested: core.Defaults(), Effective: route, Workspace: w, Owner: owner})
		if err != nil {
			return m, err
		}
		if !fresh {
			return m, fmt.Errorf("fixture command unexpectedly replayed")
		}
		finished, err := s.Finish(ctx, attempt.ID, owner.ID, "complete", output, "Synthetic timing receipt; no runtime called.")
		if err != nil {
			return m, err
		}
		if finished.State != "complete" || len(finished.Output) != m.OutputBytes {
			return m, fmt.Errorf("fixture output was not fully persisted")
		}
		selected.Revision = attempt.Revision
		if _, err = timingFilesBytes(home); err != nil {
			return m, err
		}
	}
	count := 0
	for {
		page, err := s.ListTasks(ctx, 1000, count)
		if err != nil {
			return m, err
		}
		count += len(page)
		if len(page) < 1000 {
			break
		}
	}
	if count != m.Tasks {
		return m, fmt.Errorf("fixture task count: got %d, want %d", count, m.Tasks)
	}
	if err = s.Close(); err != nil {
		return m, err
	}
	closed = true
	db, err := os.Open(filepath.Join(home, "state", "state.db"))
	if err != nil {
		return m, err
	}
	h := sha256.New()
	m.DatabaseBytes, err = io.Copy(h, db)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		return m, fmt.Errorf("fixture database checksum: read=%v close=%v", err, closeErr)
	}
	m.DatabaseSHA256 = hex.EncodeToString(h.Sum(nil))
	m.FilesBytes, err = timingFilesBytes(home)
	if err != nil {
		return m, err
	}
	m.TaskID, m.DraftTasks = selected.ID, m.Tasks-1
	m.OutputTotal = int64(m.Attempts) * int64(m.OutputBytes)
	m.SeededAt = time.Now().UTC().Format(time.RFC3339Nano)
	m.Status = "complete"
	if err = writeMarker(); err != nil {
		return m, err
	}
	return m, nil
}

// The ceiling includes SQLite sidecars. Stop and leave the partial marker if a
// future schema/dependency makes this bounded fixture unexpectedly large.
func timingFilesBytes(home string) (int64, error) {
	var total int64
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked file in synthetic fixture")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > timingMaxBytes {
			return fmt.Errorf("fixture exceeded its 500 MiB ceiling; partial home retained")
		}
		return nil
	})
	return total, err
}

func TestTimingFixtureGuardsAndSmallSeed(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := seedTimingFixture(existing, "small", timingMinFree); err == nil {
		t.Fatal("accepted an existing home")
	}
	if _, err := os.Stat(filepath.Join(existing, timingMarker)); !os.IsNotExist(err) {
		t.Fatal("existing home was mutated", err)
	}
	newHome := filepath.Join(root, "new")
	if _, err := seedTimingFixture(newHome, "small", timingMinFree-1); err == nil {
		t.Fatal("accepted inadequate free-space observation")
	}
	if _, err := seedTimingFixture(newHome, "unbounded", timingMinFree); err == nil {
		t.Fatal("accepted unbounded fixture size")
	}
	if _, err := os.Stat(newHome); !os.IsNotExist(err) {
		t.Fatal("invalid request created state", err)
	}
	m, err := seedTimingFixture(newHome, "small", timingMinFree)
	if err != nil || m.Status != "complete" || m.Tasks != 10 || m.Attempts != 2 || m.OutputTotal != 8192 || m.DatabaseSHA256 == "" {
		t.Fatal(m, err)
	}
	s, err := state.Open(newHome)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, err := s.ListAttempts(context.Background(), m.TaskID, 0, 10)
	if err != nil || len(page.Attempts) != 2 || len(page.Attempts[0].Output) != 4096 {
		t.Fatal("seeded receipt not readable", page, err)
	}
}
