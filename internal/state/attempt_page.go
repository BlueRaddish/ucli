package state

import (
	"context"

	"ucli.local/ucli/internal/core"
)

type AttemptPage struct {
	Attempts []core.Attempt `json:"attempts"`
	// Zero means no older page. Otherwise pass this exclusive revision cursor
	// to ListAttempts with the same task ID, even if newer attempts arrive.
	NextBeforeRevision int `json:"next_before_revision"`
}

// ListAttempts returns the most recent limit attempts older than beforeRevision,
// each page ordered oldest to newest. A zero cursor selects the latest page.
// Revisions are unique per task, immutable and may have gaps from draft edits.
// It does not change the full-history Attempts API or silently trim a large page.
func (s *Store) ListAttempts(ctx context.Context, taskID string, beforeRevision, limit int) (AttemptPage, error) {
	if !validID(taskID) || beforeRevision < 0 || limit < 1 || limit > maxHistoryRows {
		return AttemptPage{}, problem("invalid_input", "Attempt pagination requires a task ID, limit 1-1000 and nonnegative before revision.")
	}
	page := AttemptPage{Attempts: []core.Attempt{}}
	err := s.read(ctx, func(conn executor) error {
		// Existence needs only the indexed ID; a large task draft must not block
		// access to an independently bounded attempt page.
		var id string
		if err := conn.QueryRowContext(ctx, "SELECT id FROM tasks WHERE id=?", taskID).Scan(&id); err != nil {
			return err
		}
		query := "SELECT body,revision,id FROM (SELECT body,revision,id FROM attempts WHERE task_id=?"
		args := []any{taskID}
		if beforeRevision > 0 {
			query += " AND revision<?"
			args = append(args, beforeRevision)
		}
		query += " ORDER BY revision DESC,id DESC LIMIT ?) ORDER BY revision,id"
		args = append(args, limit)
		rows, err := boundedRows(ctx, conn, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var id string
			var revision int
			var attempt core.Attempt
			if err = rows.Scan(&raw, &revision, &id); err != nil {
				return err
			}
			if err = decode(raw, &attempt); err != nil {
				return err
			}
			if revision < 1 || attempt.Revision != revision || attempt.ID != id || attempt.TaskID != taskID {
				return problem("state_corrupt", "An attempt history record disagrees with its indexed identity or revision.")
			}
			page.Attempts = append(page.Attempts, attempt)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if len(page.Attempts) > 0 {
			oldest := page.Attempts[0].Revision
			var more bool
			if err = conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM attempts WHERE task_id=? AND revision<?)", taskID, oldest).Scan(&more); err != nil {
				return err
			}
			if more {
				page.NextBeforeRevision = oldest
			}
		}
		return nil
	})
	if err != nil {
		return AttemptPage{}, err
	}
	return page, nil
}
