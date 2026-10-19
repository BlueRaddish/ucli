package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxFrame = 64 * 1024

type Config struct {
	Marker string `json:"marker"`
}
type Service struct {
	db            *sql.DB
	root, version string
}
type Result struct {
	Type        string             `json:"type"`
	ID          string             `json:"id"`
	Host        string             `json:"host"`
	SQLite      string             `json:"sqlite"`
	Status      string             `json:"status"`
	Error       string             `json:"error,omitempty"`
	StderrBytes int64              `json:"stderr_bytes"`
	Frames      int                `json:"frames"`
	DurationMS  float64            `json:"duration_ms"`
	CancelMS    float64            `json:"cancel_ms,omitempty"`
	AcceptMS    float64            `json:"accept_ms"`
	SpawnMS     float64            `json:"spawn_ms"`
	SpawnStages map[string]float64 `json:"spawn_stages,omitempty"`
}

func openService(root string) (*Service, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("fixture config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Marker != "synthetic-p01" {
		return nil, errors.New("invalid fixture marker")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Service{db: db, root: root}
	if err = db.QueryRow("select sqlite_version()").Scan(&s.version); err != nil {
		db.Close()
		return nil, err
	}
	if !fixedSQLite(s.version) {
		db.Close()
		return nil, fmt.Errorf("SQLite %s does not meet WAL fix requirement", s.version)
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=1000", "PRAGMA synchronous=FULL", "CREATE TABLE IF NOT EXISTS attempts (id TEXT PRIMARY KEY, host TEXT NOT NULL, status TEXT NOT NULL)"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}
func fixedSQLite(v string) bool {
	p := strings.Split(v, ".")
	if len(p) < 3 {
		return false
	}
	a, e1 := strconv.Atoi(p[0])
	b, e2 := strconv.Atoi(p[1])
	c, e3 := strconv.Atoi(p[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return false
	}
	return a > 3 || (a == 3 && (b > 51 || (b == 51 && c >= 3) || (b == 50 && c >= 7) || (b == 44 && c >= 6)))
}
func (s *Service) Close() { s.db.Close() }

func (s *Service) Execute(parent context.Context, req Request, host string, emit func(Event) error) Result {
	start := time.Now()
	result := Result{Type: "result", ID: req.ID, Host: host, SQLite: s.version, Status: "failed"}
	persisted := false
	finish := func(err error) Result {
		if persisted {
			if _, saveErr := s.db.Exec("UPDATE attempts SET status=? WHERE id=?", result.Status, req.ID); saveErr != nil {
				err = errors.Join(err, fmt.Errorf("persist outcome: %w", saveErr))
				result.Status = "failed"
			}
		}
		result.DurationMS = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			result.Error = err.Error()
		}
		return result
	}
	if req.ID == "" || len(req.ID) > 128 || req.TimeoutMS < 1 || req.TimeoutMS > 60000 || req.DelayMS < 0 || req.DelayMS > 2000 {
		return finish(errors.New("invalid bounded request"))
	}
	tx, err := s.db.BeginTx(parent, nil)
	if err != nil {
		return finish(err)
	}
	if _, err = tx.Exec("INSERT INTO attempts(id,host,status) VALUES(?,?,?)", req.ID, host, "accepted"); err != nil {
		tx.Rollback()
		return finish(err)
	}
	if err = tx.Commit(); err != nil {
		return finish(err)
	}
	persisted = true
	result.AcceptMS = float64(time.Since(start).Microseconds()) / 1000
	if req.Scenario == "crash-after-accept" {
		os.Exit(7)
	}
	if err = emit(Event{Type: "accepted"}); err != nil {
		return finish(err)
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(req.TimeoutMS)*time.Millisecond)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return finish(err)
	}
	cmd := exec.Command(exe, "child", "--root", s.root, "--scenario", req.Scenario, "--delay-ms", strconv.Itoa(req.DelayMS))
	stdout := &frameWriter{emit: emit}
	stderr := &drainCount{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 300 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return finish(err)
	}
	spawnStart := time.Now()
	owner, err := startOwned(cmd)
	result.SpawnMS = float64(time.Since(spawnStart).Microseconds()) / 1000
	if err != nil {
		stdin.Close()
		return finish(err)
	}
	result.SpawnStages = owner.stages
	done := make(chan error, 1)
	go func() { done <- owner.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		cancelStart := time.Now()
		result.Status = "interrupted"
		fmt.Fprintln(stdin, "stop")
		select {
		case waitErr = <-done:
		case <-time.After(150 * time.Millisecond):
			owner.Stop()
			waitErr = <-done
		}
		result.CancelMS = float64(time.Since(cancelStart).Microseconds()) / 1000
	}
	stdin.Close()
	// Closing ownership kills remaining fixture descendants, including when the
	// main child completed first. It never searches by executable name.
	owner.Close()
	result.Frames = stdout.frames
	result.StderrBytes = stderr.n
	if result.Status != "interrupted" {
		if len(stdout.buf) != 0 {
			waitErr = errors.Join(waitErr, errors.New("unterminated output frame"))
		}
		if waitErr == nil && stdout.terminal {
			result.Status = "complete"
		} else if waitErr == nil {
			waitErr = errors.New("missing terminal result")
		}
	}
	if result.Status == "interrupted" {
		waitErr = nil
	}
	return finish(waitErr)
}

// The bounded lease fixture proves cross-process exclusion keyed by filesystem
// identity. It intentionally has no stale-owner recovery; P08 must add fenced
// owner lifetimes before this can be used for real work.
func (s *Service) Lease(workspace, mode string, holdMS int, enc *json.Encoder) error {
	if (mode != "read" && mode != "write") || holdMS < 1 || holdMS > 5000 {
		return errors.New("invalid fixture lease")
	}
	identity, err := workspaceIdentity(workspace)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec("CREATE TABLE IF NOT EXISTS leases(owner TEXT PRIMARY KEY, workspace TEXT, mode TEXT)"); err != nil {
		return err
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	rollback := func() { conn.ExecContext(context.Background(), "ROLLBACK") }
	var conflicts int
	if err = conn.QueryRowContext(context.Background(), "SELECT count(*) FROM leases WHERE workspace=? AND (?='write' OR mode='write')", identity.ID, mode).Scan(&conflicts); err != nil {
		rollback()
		return err
	}
	if conflicts != 0 {
		rollback()
		return errors.New("workspace contention")
	}
	owner := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	if _, err = conn.ExecContext(context.Background(), "INSERT INTO leases VALUES(?,?,?)", owner, identity.ID, mode); err != nil {
		rollback()
		return err
	}
	if _, err = conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		rollback()
		return err
	}
	defer conn.ExecContext(context.Background(), "DELETE FROM leases WHERE owner=?", owner)
	if err = enc.Encode(map[string]any{"type": "lease_acquired", "workspace": identity, "mode": mode}); err != nil {
		return err
	}
	time.Sleep(time.Duration(holdMS) * time.Millisecond)
	return nil
}

type frameWriter struct {
	buf      []byte
	emit     func(Event) error
	frames   int
	terminal bool
}

func (w *frameWriter) Write(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\n')
		take := len(data)
		if idx >= 0 {
			take = idx + 1
		}
		if len(w.buf)+take > maxFrame {
			return 0, errors.New("fixture frame exceeds 64 KiB")
		}
		w.buf = append(w.buf, data[:take]...)
		data = data[take:]
		if idx >= 0 {
			var event Event
			if err := json.Unmarshal(w.buf, &event); err != nil {
				return 0, fmt.Errorf("malformed frame: %w", err)
			}
			if event.Type == "" {
				return 0, errors.New("event has no type")
			}
			if err := w.emit(event); err != nil {
				return 0, err
			}
			w.frames++
			w.terminal = w.terminal || event.Type == "terminal_result"
			w.buf = w.buf[:0]
		}
	}
	return n, nil
}
