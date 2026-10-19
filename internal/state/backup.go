package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"modernc.org/sqlite"
	"ucli.local/ucli/internal/core"
)

// BackupReceipt identifies a standalone SQLite snapshot. It contains task data,
// including prompts/results and unsettled ownership, but no credential store.
// Integrity means a SQLite structural audit, not application-semantic validation.
type BackupReceipt struct {
	Version           int       `json:"version"`
	Schema            int       `json:"schema"`
	Path              string    `json:"path"`
	SHA256            string    `json:"sha256"`
	Bytes             int64     `json:"bytes"`
	CreatedAt         time.Time `json:"created_at"`
	Tasks             int64     `json:"tasks"`
	Attempts          int64     `json:"attempts"`
	Events            int64     `json:"events"`
	UnsettledAttempts int64     `json:"unsettled_attempts"`
	Warnings          []string  `json:"warnings,omitempty"`
}

func backupURI(path string, query url.Values) string {
	path = filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}
	return u.String()
}

func backupTarget(destination, source string) (string, error) {
	if !utf8.ValidString(destination) || len(destination) > 32768 || strings.ContainsRune(destination, 0) || !filepath.IsAbs(destination) || strings.Contains(destination, "://") || strings.HasPrefix(destination, `\\`) || strings.HasPrefix(destination, "//") {
		return "", problem("invalid_input", "Backup destination must be a new absolute local file outside the live state directory.")
	}
	destination = filepath.Clean(destination)
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return "", problem("invalid_input", "The backup parent directory must already exist and be accessible.")
	}
	if err = localPath(parent); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && strings.Contains(filepath.Base(destination), ":") {
		return "", problem("invalid_input", "A backup cannot use an alternate file stream.")
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", err
	}
	sourceDir := filepath.Dir(source)
	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return "", err
	}
	// Check physical parents, including path aliases, before creating any files.
	// The whole state directory is excluded, including future WAL/SHM paths.
	for dir := parent; ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return "", err
		}
		if os.SameFile(info, sourceInfo) {
			return "", problem("invalid_input", "Backups must be outside the live state directory.")
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if _, err = os.Lstat(destination); err == nil {
		return "", core.Problem("backup_exists", "The backup destination already exists; choose a new file.", 5)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return destination, nil
}

type backupReader struct {
	ctx context.Context
	io.Reader
}

func (r backupReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

type sqliteBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

// copySnapshot is called on the schema-fenced, pinned source read transaction.
// afterStep is an internal consuming-test seam: it runs after a real SQLite page
// copy, allowing concurrent writes/cancellation without timing-based tests.
func copySnapshot(ctx context.Context, conn *sql.Conn, destination string, afterStep func() error) error {
	return conn.Raw(func(raw any) (err error) {
		native, ok := raw.(sqliteBackuper)
		if !ok {
			return errors.New("SQLite online backup is unavailable")
		}
		backup, err := native.NewBackup(backupURI(destination, url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(2000)", "synchronous(FULL)"}}))
		if err != nil {
			return err
		}
		finalized := false
		defer func() {
			if !finalized {
				err = errors.Join(err, backup.Finish())
			}
		}()
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			more, stepErr := backup.Step(64)
			if stepErr != nil {
				return stepErr
			}
			if afterStep != nil {
				if err = afterStep(); err != nil {
					return err
				}
			}
			if !more {
				break
			}
		}
		// modernc1.60.1 does not make Finish/Commit idempotent. Finalize once,
		// and observe destination close errors rather than losing them in Finish.
		finalized = true
		var destinationConn driver.Conn
		destinationConn, err = backup.Commit()
		if err != nil {
			return err
		}
		return destinationConn.Close()
	})
}

func inspectSnapshot(ctx context.Context, path string, receipt *BackupReceipt) (err error) {
	// SQLite3.53.4 discards CHECK expressions while parsing schema on a
	// read-only Btree. Audit our private copy through a read-write connection,
	// then query_only, so integrity_check actually evaluates those constraints.
	db, err := sql.Open("sqlite", backupURI(path, url.Values{"mode": {"rw"}, "_pragma": {"synchronous(FULL)", "busy_timeout(2000)"}}))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, db.Close()) }()
	// Normalize only our private snapshot to a standalone rollback-journal file.
	// The live source and its WAL mode are never changed.
	var journal string
	if err = db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&journal); err != nil {
		return err
	}
	if journal != "delete" {
		return problem("backup_unavailable", "The private backup could not become a standalone SQLite file.")
	}
	if _, err = db.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return err
	}
	snapshot := &Store{db: db}
	if err = snapshot.CheckIntegrity(ctx); err != nil {
		return err
	}
	return snapshot.read(ctx, func(conn executor) error {
		if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&receipt.Schema); err != nil {
			return err
		}
		return conn.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM tasks), (SELECT count(*) FROM attempts),
			(SELECT count(*) FROM events),
			(SELECT count(*) FROM attempts WHERE state IN ('running','unknown'))`).Scan(
			&receipt.Tasks, &receipt.Attempts, &receipt.Events, &receipt.UnsettledAttempts)
	})
}

// Backup creates a verified standalone online SQLite snapshot without copying
// live WAL/SHM files or overwriting an existing path. A pinned read transaction
// gives one consistent snapshot while other WAL writers can continue. Publication
// requires same-filesystem hard links. This is not a migration/restore barrier,
// an automatic resume instruction, or a physical power-loss certification.
func (s *Store) Backup(ctx context.Context, destination string) (receipt BackupReceipt, err error) {
	var staging, stageFile string
	published := false
	defer func() {
		if staging == "" {
			return
		}
		// Only known children of our private staging directory are removed. Never
		// recursively follow caller paths; an unexpected child leaves a warning.
		var cleanupErr error
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			if removeErr := os.Remove(stageFile + suffix); removeErr != nil && !os.IsNotExist(removeErr) {
				cleanupErr = errors.Join(cleanupErr, removeErr)
			}
		}
		cleanupErr = errors.Join(cleanupErr, os.Remove(staging))
		if cleanupErr != nil && published {
			receipt.Warnings = append(receipt.Warnings, "The verified backup was saved, but its private staging directory could not be fully removed.")
		}
		if cleanupErr != nil && !published {
			// Retain the original error code while making leftover private task
			// data visible to both library consumers and the CLI's typed error.
			original := stateError(err)
			var domain *core.Error
			if errors.As(original, &domain) {
				copy := *domain
				copy.Message += " Private backup staging could not be fully removed."
				err = &copy
			} else {
				err = problem("backup_unavailable", "Backup failed and its private staging could not be fully removed.")
			}
		}
		if err != nil {
			receipt = BackupReceipt{}
		}
	}()
	if err = ctx.Err(); err != nil {
		return receipt, stateError(err)
	}
	err = s.read(ctx, func(conn executor) error {
		var source string
		if err := conn.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&source); err != nil {
			return err
		}
		target, err := backupTarget(destination, source)
		if err != nil {
			return err
		}
		receipt = BackupReceipt{Version: 1, Path: target, CreatedAt: time.Now().UTC()}
		staging, err = os.MkdirTemp(filepath.Dir(target), ".ucli-snapshot-")
		if err != nil {
			return err
		}
		stageFile = filepath.Join(staging, "snapshot.db")
		if err = securePath(staging, true); err != nil {
			return err
		}
		file, err := os.OpenFile(stageFile, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		if err = securePath(stageFile, false); err != nil {
			return err
		}
		return copySnapshot(ctx, conn.(*sql.Conn), stageFile, nil)
	})
	if err != nil {
		return BackupReceipt{}, err
	}
	if err = inspectSnapshot(ctx, stageFile, &receipt); err != nil {
		return BackupReceipt{}, stateError(err)
	}
	file, err := os.OpenFile(stageFile, os.O_RDWR, 0)
	if err != nil {
		return BackupReceipt{}, stateError(err)
	}
	hash := sha256.New()
	receipt.Bytes, err = io.CopyBuffer(hash, backupReader{ctx, file}, make([]byte, 64*1024))
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return BackupReceipt{}, stateError(err)
	}
	receipt.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if err = ctx.Err(); err != nil {
		return BackupReceipt{}, stateError(err)
	}
	// Link is an atomic create-if-absent, unlike rename-over-existing. The staged
	// file is private; no concurrent writer can change its verified contents.
	if err = os.Link(stageFile, receipt.Path); err != nil {
		if os.IsExist(err) {
			return BackupReceipt{}, core.Problem("backup_exists", "The backup destination already exists; choose a new file.", 5)
		}
		return BackupReceipt{}, problem("backup_unavailable", "The backup could not be published. Use a new file on a local filesystem supporting hard links.")
	}
	published = true
	return receipt, nil
}
