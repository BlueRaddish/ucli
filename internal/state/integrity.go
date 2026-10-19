package state

import "context"

// CheckIntegrity explicitly scans the full SQLite database in a schema-fenced
// read transaction. Cost grows with database size; normal Open does not run it.
// Epoch2 also audits materialized task metadata against its original task bodies.
// This is not a full semantic audit of attempts/events or a foreign-key check.
// No raw database diagnostics enter public errors.
func (s *Store) CheckIntegrity(ctx context.Context) error {
	return s.read(ctx, func(conn executor) error {
		rows, err := conn.QueryContext(ctx, "PRAGMA integrity_check")
		if err != nil {
			return err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var result string
			if err = rows.Scan(&result); err != nil {
				return err
			}
			if result != "ok" {
				return problem("state_corrupt", "Local state failed its integrity check. Preserve it for recovery.")
			}
			count++
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if count != 1 {
			return problem("state_corrupt", "Local state returned no unique integrity result. Preserve it for recovery.")
		}
		if err = rows.Close(); err != nil {
			return err
		}
		var epoch int
		if err = conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&epoch); err != nil {
			return err
		}
		if epoch == 2 {
			return auditMetadata(ctx, conn)
		}
		return nil
	})
}
