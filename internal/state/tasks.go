package state

import (
	"context"
	"database/sql"
	"time"

	"ucli.local/ucli/internal/core"
)

// SaveDraft is a CAS mutation. Drafting while work runs cannot change that attempt.
func (s *Store) SaveDraft(ctx context.Context, task core.Task, expectedRevision int) (core.Task, error) {
	if expectedRevision < 0 || !validText(task.Draft, maxInput) || !validText(task.Title, 1024) {
		return core.Task{}, problem("invalid_input", "The draft or expected revision is invalid.")
	}
	if err := validateWorkspace(task.Workspace); err != nil {
		return core.Task{}, err
	}
	task.Selection = task.Selection.WithDefaults()
	if err := core.ValidateSelection(task.Selection); err != nil {
		return core.Task{}, err
	}
	var saved core.Task
	err := s.write(ctx, func(conn executor) error {
		now := time.Now().UTC()
		create := task.ID == ""
		if create {
			if expectedRevision != 0 {
				return problem("revision_conflict", "A new draft requires expected revision zero.")
			}
			task.ID = core.NewID("task")
			task.Revision = 1
			task.CreatedAt = now
			task.Status = "draft"
		} else {
			current, err := loadTask(ctx, conn, task.ID)
			if err != nil {
				return err
			}
			if current.Revision != expectedRevision {
				return problem("revision_conflict", "The task changed. Reload it before saving this draft.")
			}
			if current.Workspace.ID != task.Workspace.ID {
				return problem("invalid_input", "A task cannot move to another workspace. Start a new task.")
			}
			task.Revision = current.Revision + 1
			task.CreatedAt = current.CreatedAt
			active, err := activeTask(ctx, conn, task.ID)
			if err != nil {
				return err
			}
			task.Status = "draft"
			if active {
				task.Status = current.Status
			}
		}
		if task.Title == "" {
			task.Title = title(task.Draft)
		}
		task.UpdatedAt = now
		if err := saveTask(ctx, conn, task, create); err != nil {
			return err
		}
		saved = task
		return nil
	})
	if err != nil {
		return core.Task{}, err
	}
	return saved, nil
}

func (s *Store) GetTask(ctx context.Context, id string) (core.Task, error) {
	var task core.Task
	err := s.read(ctx, func(conn executor) error { var err error; task, err = loadTask(ctx, conn, id); return err })
	return task, err
}

func (s *Store) ListTasks(ctx context.Context, limit, offset int) ([]core.Task, error) {
	if limit < 1 || limit > 1000 || offset < 0 {
		return nil, problem("invalid_input", "Task pagination requires limit 1–1000 and nonnegative offset.")
	}
	tasks := []core.Task{}
	err := s.read(ctx, func(conn executor) error {
		rows, err := boundedRows(ctx, conn, "SELECT body FROM tasks ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?", limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var task core.Task
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			if err = decode(raw, &task); err != nil {
				return err
			}
			tasks = append(tasks, task)
		}
		return rows.Err()
	})
	return tasks, err
}

func (s *Store) GetAttempt(ctx context.Context, id string) (core.Attempt, error) {
	var attempt core.Attempt
	err := s.read(ctx, func(conn executor) error { var err error; attempt, err = loadAttempt(ctx, conn, id); return err })
	return attempt, err
}

// GetCommand returns the original intent fingerprint; route resolution is not needed for replay.
func (s *Store) GetCommand(ctx context.Context, id string) (core.Attempt, string, error) {
	var attempt core.Attempt
	var hash string
	err := s.read(ctx, func(conn executor) error {
		var raw sql.NullString
		if err := conn.QueryRowContext(ctx, "SELECT CASE WHEN length(CAST(body AS BLOB))<=? THEN body END,intent_hash FROM attempts WHERE command_id=?", maxHistoryBytes, id).Scan(&raw, &hash); err != nil {
			return err
		}
		if !raw.Valid {
			return historyTooLarge()
		}
		return decode(raw.String, &attempt)
	})
	return attempt, hash, err
}

// Attempts returns immutable accepted revisions in oldest-to-newest order.
func (s *Store) Attempts(ctx context.Context, taskID string) ([]core.Attempt, error) {
	attempts := []core.Attempt{}
	err := s.read(ctx, func(conn executor) error {
		if _, err := loadTask(ctx, conn, taskID); err != nil {
			return err
		}
		rows, err := boundedRows(ctx, conn, "SELECT body FROM attempts WHERE task_id=? ORDER BY revision,id LIMIT ?", taskID, maxHistoryRows+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var attempt core.Attempt
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			if err = decode(raw, &attempt); err != nil {
				return err
			}
			attempts = append(attempts, attempt)
		}
		return rows.Err()
	})
	return attempts, err
}
