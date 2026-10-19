package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

type MigrationReceipt struct {
	Version    int `json:"version"`
	FromSchema int `json:"from_schema"`
	ToSchema   int `json:"to_schema"`
	// Status is applied, not_applied, unknown (commit acknowledgment uncertain),
	// or unchanged (already at epoch2). A backup certificate proves no commit.
	Status      string        `json:"status"`
	Backup      BackupReceipt `json:"backup"`
	ReceiptPath string        `json:"receipt_path,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
}

type migrationCertificate struct {
	Version    int           `json:"version"`
	FromSchema int           `json:"from_schema"`
	ToSchema   int           `json:"to_schema"`
	Backup     BackupReceipt `json:"backup"`
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := s.read(ctx, func(conn executor) error { return conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) })
	return version, err
}

// Migrate explicitly advances schema1 to schema2 behind SQLite's writer barrier.
// It never changes existing/new homes during Open. Once a verified backup has
// been published it is retained on every later failure, and returned with error.
// The certificate at newBackupPath+".receipt.json" describes only that snapshot;
// it cannot prove whether migration subsequently committed. Inspect SchemaVersion
// after an unknown status; never infer rollback, downgrade or retry work.
func (s *Store) Migrate(ctx context.Context, newBackupPath string) (MigrationReceipt, error) {
	return s.migrate(ctx, newBackupPath, nil)
}

// afterStage is a private fault/child-process synchronization seam. Every stage
// follows a real operation under the real SQLite barrier; it is not public API.
func (s *Store) migrate(ctx context.Context, newBackupPath string, afterStage func(string) error) (receipt MigrationReceipt, err error) {
	receipt = MigrationReceipt{Version: 1, ToSchema: 2, Status: "not_applied"}
	if ctx == nil {
		return receipt, problem("invalid_input", "Migration requires a context.")
	}
	readyToCommit := false
	stage := func(name string) error {
		if afterStage != nil {
			return afterStage(name)
		}
		return nil
	}
	err = s.write(ctx, func(conn executor) error {
		if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&receipt.FromSchema); err != nil {
			return err
		}
		if receipt.FromSchema == 2 {
			receipt.Status = "unchanged"
			return nil
		}
		var active bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM attempts WHERE state IN ('running','unknown')) OR EXISTS(SELECT 1 FROM leases)`).Scan(&active); err != nil {
			return err
		}
		if active {
			return problem("migration_busy", "Migration requires no running or unknown attempts and no workspace leases. Reconcile explicitly; no work was stopped.")
		}
		var source string
		if err := conn.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&source); err != nil {
			return err
		}
		target, err := backupTarget(newBackupPath, source)
		if err != nil {
			return err
		}
		certificatePath, err := backupTarget(target+".receipt.json", source)
		if err != nil {
			return err
		}
		// Detect a foreign/partial layout before creating an otherwise unnecessary
		// backup. SQLite transactional DDL leaves none of these after rollback.
		for _, object := range metadataObjects {
			var count int
			if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name=?", object.name).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return metadataCorrupt()
			}
		}
		if err := stage("barrier"); err != nil {
			return err
		}
		// No source write has occurred after BEGIN IMMEDIATE. The separate reader
		// snapshots the exact committed state protected by this writer barrier.
		// Calling Open or backing up from this write connection would deadlock.
		receipt.Backup, err = migrationBackup(ctx, source, target)
		if err != nil {
			return err
		}
		receipt.Warnings = append(receipt.Warnings, receipt.Backup.Warnings...)
		warning, err := publishMigrationCertificate(ctx, certificatePath, migrationCertificate{1, 1, 2, receipt.Backup})
		if warning != "" {
			receipt.Warnings = append(receipt.Warnings, warning)
		}
		if err != nil {
			return err
		}
		receipt.ReceiptPath = certificatePath
		if err := stage("backup"); err != nil {
			return err
		}
		for _, object := range metadataObjects {
			if _, err := conn.ExecContext(ctx, object.sql); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO task_metadata SELECT * FROM task_metadata_source"); err != nil {
			return err
		}
		if err := stage("backfill"); err != nil {
			return err
		}
		if err := auditMetadata(ctx, conn); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA user_version=2"); err != nil {
			return err
		}
		if err := stage("commit"); err != nil {
			return err
		}
		readyToCommit = true
		return nil
	})
	if receipt.Status == "unchanged" {
		return receipt, err
	}
	if err == nil {
		receipt.Status = "applied"
	} else if readyToCommit {
		receipt.Status = "unknown"
	}
	return receipt, err
}

func migrationBackup(ctx context.Context, source, target string) (receipt BackupReceipt, err error) {
	db, err := sql.Open("sqlite", backupURI(source, url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(2000)", "query_only(1)"}}))
	if err != nil {
		return receipt, stateError(err)
	}
	db.SetMaxOpenConns(1)
	defer func() { err = stateError(errors.Join(err, db.Close())) }()
	reader := &Store{db: db}
	return reader.Backup(ctx, target)
}

func publishMigrationCertificate(ctx context.Context, path string, certificate migrationCertificate) (warning string, err error) {
	raw, err := json.MarshalIndent(certificate, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ucli-migration-receipt-")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer func() {
		if removeErr := os.Remove(temporary); removeErr != nil && !os.IsNotExist(removeErr) {
			if warning != "" {
				warning += " "
			}
			warning += "A private migration certificate staging file could not be removed."
		}
	}()
	if err = securePath(temporary, false); err == nil {
		if err = ctx.Err(); err == nil {
			_, err = file.Write(append(raw, '\n'))
		}
		if err == nil {
			err = file.Sync()
		}
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return warning, stateError(err)
	}
	if err = ctx.Err(); err != nil {
		return warning, stateError(err)
	}
	if err = os.Link(temporary, path); err != nil {
		if os.IsExist(err) {
			return warning, problem("migration_conflict", "The migration backup certificate already exists; the verified backup was retained.")
		}
		return warning, problem("backup_unavailable", "The verified backup was retained, but its migration certificate could not be published.")
	}
	// Both published links share this parent. File Sync alone cannot establish
	// directory-entry durability. Some platforms/filesystems cannot sync a
	// directory; expose that limit without deleting the verified recovery files.
	parent, openErr := os.Open(filepath.Dir(path))
	if openErr != nil {
		warning = "The backup and certificate were published, but their directory-entry durability could not be verified."
	} else if syncErr := errors.Join(parent.Sync(), parent.Close()); syncErr != nil {
		warning = "The backup and certificate were published, but directory synchronization is unsupported or failed; power-loss durability is unverified."
	}
	return warning, nil
}
