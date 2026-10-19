// Package state owns local durable task intent, receipts and cooperative leases.
// It does not establish that a native process or remote inference has stopped.
package state

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"modernc.org/sqlite"
	"ucli.local/ucli/internal/core"
)

const schemaVersion = 2
const oldestSchemaVersion = 1
const maxText = 64 * 1024
const maxInput = core.MaxInputBytes
const maxOutput = core.MaxOutputBytes
const maxHistoryBytes = 16 * 1024 * 1024
const maxHistoryRows = 1000

type Store struct{ db *sql.DB }

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func problem(code, message string) error {
	exit := 9
	switch code {
	case "invalid_input", "not_found":
		exit = 2
	case "revision_conflict", "command_conflict", "task_busy", "workspace_busy", "owner_mismatch", "invalid_state", "native_context_conflict", "migration_busy", "migration_conflict":
		exit = 5
	}
	return core.Problem(code, message, exit)
}

func stateError(err error) error {
	if err == nil {
		return nil
	}
	var domain *core.Error
	if errors.As(err, &domain) {
		return err
	}
	// Preserve explicit cancellation for every consumer's common130/124 path.
	// Cancellation is not evidence that a transaction or external work stopped;
	// callers still inspect durable receipts before deciding whether to retry.
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, sql.ErrNoRows) {
		return problem("not_found", "The requested local record does not exist.")
	}
	var native *sqlite.Error
	if errors.As(err, &native) {
		switch native.Code() & 255 {
		case 5, 6:
			e := core.Problem("state_busy", "Local state is busy. Retry with the same command ID.", 9)
			e.Retry = "safe"
			return e
		case 11, 26:
			return problem("state_corrupt", "Local state failed its integrity check. Preserve it for recovery.")
		}
	}
	return problem("state_unavailable", "Local state could not be read or saved. Preserve the command ID and inspect its receipt before retrying.")
}

// Open uses a dedicated state leaf, never a credential store, workspace or live DB copy.
// Existing newer/foreign schemas are refused, not migrated or overwritten.
// It validates startup prerequisites, not every database page; use CheckIntegrity
// for an explicit full SQLite audit.
func Open(home string) (*Store, error) {
	if home == "" || strings.Contains(home, "://") || strings.HasPrefix(strings.ToLower(home), "file:") || strings.HasPrefix(home, `\\`) || strings.HasPrefix(home, "//") {
		return nil, problem("invalid_input", "State home must be a local filesystem directory.")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, stateError(err)
	}
	if err = localPath(abs); err != nil {
		return nil, stateError(err)
	}
	directory := filepath.Join(abs, "state")
	if err = rejectLink(directory); err != nil {
		return nil, stateError(err)
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, stateError(err)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, stateError(err)
	}
	if err = localPath(resolved); err != nil {
		return nil, stateError(err)
	}
	if err = securePath(directory, true); err != nil {
		return nil, stateError(err)
	}
	path := filepath.Join(directory, "state.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err = rejectLink(path + suffix); err != nil {
			return nil, stateError(err)
		}
		if suffix != "" {
			if _, statErr := os.Stat(path + suffix); statErr == nil {
				if err = securePath(path+suffix, false); err != nil {
					return nil, stateError(err)
				}
			} else if !os.IsNotExist(statErr) {
				return nil, stateError(statErr)
			}
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, stateError(err)
	}
	if err = file.Close(); err != nil {
		return nil, stateError(err)
	}
	if err = securePath(path, false); err != nil {
		return nil, stateError(err)
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(2000)", "foreign_keys(1)", "synchronous(FULL)"}}
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, stateError(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = s.initialize(ctx); err != nil {
		db.Close()
		return nil, stateError(err)
	}
	return s, nil
}

func rejectLink(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return problem("invalid_input", "State files and the state directory cannot be symbolic links.")
	}
	return nil
}

func fixedSQLite(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	patch, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return false
	}
	return major > 3 || major == 3 && (minor > 51 || minor == 51 && patch >= 3 || minor == 50 && patch >= 7 || minor == 44 && patch >= 6)
}

func (s *Store) initialize(ctx context.Context) error {
	var version string
	if err := s.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return err
	}
	if !fixedSQLite(version) {
		return problem("state_unavailable", "The SQLite engine lacks the required WAL fix.")
	}
	err := s.transaction(ctx, true, false, func(conn executor) error {
		var epoch int
		if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&epoch); err != nil {
			return err
		}
		if epoch > schemaVersion {
			return problem("schema_newer", "Local state was created by a newer UCLI. Use a compatible version.")
		}
		if epoch >= oldestSchemaVersion && epoch <= schemaVersion {
			return nil
		}
		if epoch != 0 {
			return problem("state_corrupt", "Local state has an unsupported schema version.")
		}
		var tables int
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return problem("state_corrupt", "An unrecognized database occupies the state path; it was not changed.")
		}
		_, err := conn.ExecContext(ctx, schema)
		return err
	})
	if err != nil {
		return err
	}
	var journal string
	if err = s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
		return err
	}
	if journal != "wal" {
		return problem("state_unavailable", "Local state could not enable WAL journaling.")
	}
	// New homes deliberately remain epoch1. Existing homes are never migrated by
	// Open; only the explicit backed-up maintenance operation advances the epoch.
	return s.read(ctx, func(conn executor) error {
		_, err := conn.ExecContext(ctx, "SELECT t.id,t.revision,a.intent_hash,a.owner_id,e.sequence,l.mode FROM tasks t LEFT JOIN attempts a ON a.task_id=t.id LEFT JOIN events e ON e.attempt_id=a.id LEFT JOIN leases l ON l.attempt_id=a.id LIMIT 0")
		if err != nil {
			return problem("state_corrupt", "The local schema does not match its declared version. Preserve it for recovery.")
		}
		return nil
	})
}

const schema = `
CREATE TABLE tasks (id TEXT PRIMARY KEY, revision INTEGER NOT NULL CHECK(revision>0), updated_at TEXT NOT NULL, body TEXT NOT NULL);
CREATE INDEX tasks_recent ON tasks(updated_at DESC,id DESC);
CREATE TABLE attempts (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), command_id TEXT NOT NULL UNIQUE, intent_hash TEXT NOT NULL, revision INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('running','complete','failed','interrupted','unknown')), owner_id TEXT NOT NULL, started_at TEXT NOT NULL, body TEXT NOT NULL);
CREATE UNIQUE INDEX one_active_attempt ON attempts(task_id) WHERE state IN ('running','unknown');
CREATE UNIQUE INDEX attempts_by_task ON attempts(task_id,revision);
CREATE TABLE events (attempt_id TEXT NOT NULL REFERENCES attempts(id), sequence INTEGER NOT NULL CHECK(sequence>0), body TEXT NOT NULL, PRIMARY KEY(attempt_id,sequence));
CREATE TABLE leases (attempt_id TEXT PRIMARY KEY REFERENCES attempts(id), workspace_id TEXT NOT NULL, mode TEXT NOT NULL CHECK(mode IN ('read','write')));
CREATE INDEX workspace_leases ON leases(workspace_id,mode);
PRAGMA user_version=1;
`

func (s *Store) Close() error { return stateError(s.db.Close()) }

func (s *Store) transaction(ctx context.Context, write, checkEpoch bool, action func(executor) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	begin := "BEGIN"
	if write {
		begin = "BEGIN IMMEDIATE"
	}
	if _, err = conn.ExecContext(ctx, begin); err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, rollbackErr := conn.ExecContext(rollbackCtx, "ROLLBACK"); rollbackErr != nil {
			// Never reuse a connection whose transactional state is uncertain.
			conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if checkEpoch {
		var epoch int
		if err = conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&epoch); err != nil {
			return err
		}
		if epoch > schemaVersion {
			return problem("schema_newer", "Local state changed to a newer schema. This writer has stopped.")
		}
		if epoch < oldestSchemaVersion {
			return problem("state_corrupt", "Local state schema changed unexpectedly. This writer has stopped.")
		}
		if epoch == 2 {
			if err = checkMetadataSchema(ctx, conn); err != nil {
				return err
			}
		}
	}
	if err = action(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	committed = err == nil
	return err
}

func (s *Store) read(ctx context.Context, action func(executor) error) error {
	return stateError(s.transaction(ctx, false, true, action))
}
func (s *Store) write(ctx context.Context, action func(executor) error) error {
	return stateError(s.transaction(ctx, true, true, action))
}

func encode(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err == nil && len(raw) > maxHistoryBytes {
		return "", historyTooLarge()
	}
	return string(raw), err
}
func historyTooLarge() error {
	return problem("history_too_large", "Local history exceeds 16 MiB or 1000 rows. Request a smaller history page.")
}

// A preflight in the same read transaction caps aggregate allocations before bodies
// are fetched. Full attempt history has a deliberate 1000-row ceiling;
// ListAttempts offers explicit pages without truncating the full-history API.
func boundedRows(ctx context.Context, conn executor, query string, args ...any) (*sql.Rows, error) {
	var count, bytes int64
	if err := conn.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(length(CAST(body AS BLOB))),0) FROM ("+query+")", args...).Scan(&count, &bytes); err != nil {
		return nil, err
	}
	if count > maxHistoryRows || bytes > maxHistoryBytes {
		return nil, historyTooLarge()
	}
	return conn.QueryContext(ctx, query, args...)
}
func decode(raw string, target any) error {
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return problem("state_corrupt", "A local record cannot be decoded; preserve the database for recovery.")
	}
	return nil
}
func loadTask(ctx context.Context, conn executor, id string) (core.Task, error) {
	var task core.Task
	var raw sql.NullString
	err := conn.QueryRowContext(ctx, "SELECT CASE WHEN length(CAST(body AS BLOB))<=? THEN body END FROM tasks WHERE id=?", maxHistoryBytes, id).Scan(&raw)
	if err == nil {
		if !raw.Valid {
			return core.Task{}, historyTooLarge()
		}
		err = decode(raw.String, &task)
	}
	return task, err
}
func loadAttempt(ctx context.Context, conn executor, id string) (core.Attempt, error) {
	var attempt core.Attempt
	var raw sql.NullString
	err := conn.QueryRowContext(ctx, "SELECT CASE WHEN length(CAST(body AS BLOB))<=? THEN body END FROM attempts WHERE id=?", maxHistoryBytes, id).Scan(&raw)
	if err == nil {
		if !raw.Valid {
			return core.Attempt{}, historyTooLarge()
		}
		err = decode(raw.String, &attempt)
	}
	return attempt, err
}
func saveTask(ctx context.Context, conn executor, task core.Task, create bool) error {
	raw, err := encode(task)
	if err != nil {
		return err
	}
	if create {
		_, err = conn.ExecContext(ctx, "INSERT INTO tasks(id,revision,updated_at,body) VALUES(?,?,?,?)", task.ID, task.Revision, task.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), raw)
	} else {
		_, err = conn.ExecContext(ctx, "UPDATE tasks SET revision=?,updated_at=?,body=? WHERE id=?", task.Revision, task.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), raw, task.ID)
	}
	return err
}
func saveAttempt(ctx context.Context, conn executor, attempt core.Attempt) error {
	raw, err := encode(attempt)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "UPDATE attempts SET state=?,body=? WHERE id=?", attempt.State, raw, attempt.ID)
	return err
}
func activeTask(ctx context.Context, conn executor, id string) (bool, error) {
	var count int
	err := conn.QueryRowContext(ctx, "SELECT count(*) FROM attempts WHERE task_id=? AND state IN ('running','unknown')", id).Scan(&count)
	return count != 0, err
}
func validID(id string) bool {
	return len(id) > 0 && len(id) <= 256 && utf8.ValidString(id) && !strings.ContainsAny(id, "\x00\r\n\t")
}
func validText(text string, limit int) bool {
	return len(text) <= limit && utf8.ValidString(text) && !strings.ContainsRune(text, 0)
}
func validPermission(permission string) bool {
	return permission == "read-only" || permission == "workspace-write" || permission == "unrestricted" || permission == "sandbox"
}
func validateWorkspace(workspace core.Workspace) error {
	if !validID(workspace.ID) || workspace.Root == "" || workspace.Dir == "" || !validText(workspace.Root, 32768) || !validText(workspace.Dir, 32768) {
		return problem("invalid_input", "A resolved workspace identity and directory are required.")
	}
	return nil
}
func title(input string) string {
	line := strings.TrimSpace(strings.SplitN(input, "\n", 2)[0])
	letters := []rune(line)
	if len(letters) > 80 {
		line = string(letters[:80])
	}
	if line == "" {
		return "Untitled task"
	}
	return line
}
