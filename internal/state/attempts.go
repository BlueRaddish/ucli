package state

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ucli.local/ucli/internal/core"
)

// Accept commits intent, revision, owner, workspace lease and initial event together.
// Replay is checked first, including before resolving any new effective route.
func (s *Store) Accept(ctx context.Context, cmd core.RunCommand) (core.Attempt, bool, error) {
	if !validID(cmd.ID) {
		return core.Attempt{}, false, problem("invalid_input", "A bounded command ID is required.")
	}
	var accepted core.Attempt
	created := false
	err := s.write(ctx, func(conn executor) error {
		var prior sql.NullString
		var hash string
		err := conn.QueryRowContext(ctx, "SELECT CASE WHEN length(CAST(body AS BLOB))<=? THEN body END,intent_hash FROM attempts WHERE command_id=?", maxHistoryBytes, cmd.ID).Scan(&prior, &hash)
		if err == nil {
			if hash != cmd.IntentHash() {
				return problem("command_conflict", "This command ID already belongs to different intent.")
			}
			if !prior.Valid {
				return historyTooLarge()
			}
			return decode(prior.String, &accepted)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if cmd.ExpectedRevision < 0 || !validID(cmd.Owner.ID) || cmd.Owner.PID <= 0 || cmd.Owner.StartedAt.IsZero() {
			return problem("invalid_input", "Input, expected revision and a process owner are required.")
		}
		if err = core.ValidateInput(cmd.Input); err != nil {
			return err
		}
		if err = core.ValidateSelection(cmd.Requested); err != nil {
			return err
		}
		if err = validateWorkspace(cmd.Workspace); err != nil {
			return err
		}
		if !validPermission(cmd.Requested.Permission) || !validPermission(cmd.Effective.Permission) || cmd.Requested.Permission != cmd.Effective.Permission {
			return problem("invalid_input", "Requested and effective permissions must match a supported permission.")
		}
		if cmd.Effective.Runtime == "" || cmd.Effective.Model == "" || cmd.Effective.Account == "" || cmd.Effective.Effort == "" {
			return problem("invalid_input", "An explicit effective route is required before acceptance.")
		}
		if cmd.Effective.Mode == "native" {
			// Until durable quota pools/reservations have their reviewed schema,
			// permit one native attempt per runtime in this UCLI home. The partial
			// active-attempt index bounds this scan; external native clients and
			// separate UCLI homes are outside this conservative local guard.
			var active int
			if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM attempts WHERE state IN ('running','unknown') AND json_extract(body,'$.effective.mode')='native' AND json_extract(body,'$.effective.runtime')=?", cmd.Effective.Runtime).Scan(&active); err != nil {
				return err
			}
			if active > 0 {
				return core.Problem("provider_busy", "This provider has active or uncertain work in this UCLI home. Inspect it before starting another native attempt.", 4)
			}
		}
		now := time.Now().UTC()
		var task core.Task
		createTask := cmd.TaskID == ""
		if createTask {
			if cmd.ExpectedRevision != 0 {
				return problem("revision_conflict", "A new task requires expected revision zero.")
			}
			task = core.Task{ID: core.NewID("task"), Title: title(cmd.Input), Revision: 1, CreatedAt: now, Workspace: cmd.Workspace}
		} else {
			task, err = loadTask(ctx, conn, cmd.TaskID)
			if err != nil {
				return err
			}
			if task.Revision != cmd.ExpectedRevision {
				return problem("revision_conflict", "The task changed. Reload it before submitting a follow-up.")
			}
			if task.Workspace.ID != cmd.Workspace.ID {
				return problem("invalid_input", "A task cannot move to another workspace. Start a new task.")
			}
			active, err := activeTask(ctx, conn, task.ID)
			if err != nil {
				return err
			}
			if active {
				return problem("task_busy", "This task has active or uncertain work. Inspect or reconcile it first.")
			}
			task.Revision++
		}
		mode := "read"
		if cmd.Effective.Permission != "read-only" {
			mode = "write"
		}
		var conflicts int
		if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM leases WHERE workspace_id=? AND (mode='write' OR ?='write')", cmd.Workspace.ID, mode).Scan(&conflicts); err != nil {
			return err
		}
		if conflicts > 0 {
			return problem("workspace_busy", "This workspace has conflicting active or uncertain work.")
		}
		task.Draft = ""
		task.Selection = cmd.Requested
		// A label can be reused after unlinking a connection. Follow-ups retain
		// the accepted connection ID; the immutable receipt keeps literal intent.
		if cmd.Effective.Mode == "native" && cmd.Requested.Account != "auto" {
			task.Selection.Account = cmd.Effective.Account
		}
		task.Status = "running"
		task.UpdatedAt = now
		task.Workspace = cmd.Workspace
		if err = saveTask(ctx, conn, task, createTask); err != nil {
			return err
		}
		accepted = core.Attempt{ID: core.NewID("attempt"), TaskID: task.ID, CommandID: cmd.ID, Revision: task.Revision, Input: cmd.Input,
			Requested: cmd.Requested, Effective: cmd.Effective, State: "running", Owner: cmd.Owner, StartedAt: now, Workspace: cmd.Workspace}
		raw, err := encode(accepted)
		if err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "INSERT INTO attempts(id,task_id,command_id,intent_hash,revision,state,owner_id,started_at,body) VALUES(?,?,?,?,?,?,?,?,?)",
			accepted.ID, task.ID, cmd.ID, cmd.IntentHash(), accepted.Revision, accepted.State, cmd.Owner.ID, now.Format(time.RFC3339Nano), raw); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "INSERT INTO leases(attempt_id,workspace_id,mode) VALUES(?,?,?)", accepted.ID, cmd.Workspace.ID, mode); err != nil {
			return err
		}
		if _, err = insertEvent(ctx, conn, core.Event{TaskID: task.ID, AttemptID: accepted.ID, Type: "accepted", State: "running"}); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return core.Attempt{}, false, err
	}
	return accepted, created, nil
}

func ownerCheck(attempt core.Attempt, ownerID string) error {
	if ownerID == "" || attempt.Owner.ID != ownerID {
		return problem("owner_mismatch", "The attempt belongs to a different owner instance.")
	}
	return nil
}

func terminal(state string) bool {
	return state == "complete" || state == "failed" || state == "interrupted" || state == "unknown"
}

// Finish is atomic. An unknown result retains ownership and the workspace lease.
func (s *Store) Finish(ctx context.Context, attemptID, ownerID, state, output, message string) (core.Attempt, error) {
	return s.FinishWithCause(ctx, attemptID, ownerID, state, output, message, "")
}

// FinishWithCause saves a stop reason in the terminal transaction. Unknown work
// retains its lease even when its initiating deadline or cancellation is known.
func (s *Store) FinishWithCause(ctx context.Context, attemptID, ownerID, state, output, message, cause string) (core.Attempt, error) {
	return s.FinishClassified(ctx, attemptID, ownerID, state, output, message, cause, nil)
}

// FinishClassified commits a stable failure category with the terminal receipt.
// Nil retains the historical generic failed-result behavior. Repeated finishes
// must preserve the exact class, including whether it was absent.
func (s *Store) FinishClassified(ctx context.Context, attemptID, ownerID, state, output, message, cause string, failure *core.FailureClass) (core.Attempt, error) {
	if failure != nil && state != "failed" || core.ValidateFailureClass(failure) != nil {
		return core.Attempt{}, problem("invalid_input", "Failure metadata requires confirmed failed work, a supported exit and bounded single-line identifiers.")
	}
	if failure != nil {
		copy := *failure
		failure = &copy
	}
	if (cause != "" && cause != "deadline" && cause != "canceled") || (cause != "" && state != "interrupted" && state != "unknown") {
		return core.Attempt{}, problem("invalid_input", "Stop cause requires interrupted or unknown work and a recognized reason.")
	}
	if !terminal(state) || !validText(output, maxOutput) || !validText(message, maxText) {
		return core.Attempt{}, problem("invalid_input", "The terminal state or output exceeds supported bounds.")
	}
	var finished core.Attempt
	errorMessage := message
	if state == "complete" {
		errorMessage = ""
	}
	err := s.write(ctx, func(conn executor) error {
		attempt, err := loadAttempt(ctx, conn, attemptID)
		if err != nil {
			return err
		}
		if err = ownerCheck(attempt, ownerID); err != nil {
			return err
		}
		if attempt.State != "running" {
			if attempt.State == state && attempt.Output == output && attempt.Error == errorMessage && attempt.StopCause == cause && sameFailureClass(attempt.Failure, failure) {
				if state == "complete" {
					prior, err := completionMessage(ctx, conn, attempt)
					if err != nil {
						return err
					}
					if prior != message {
						return problem("invalid_state", "A terminal receipt's informational message cannot be replaced.")
					}
				}
				finished = attempt
				return nil
			}
			return problem("invalid_state", "A terminal receipt cannot be overwritten. Reconcile uncertain work explicitly.")
		}
		attempt.State = state
		attempt.StopCause = cause
		attempt.Output = output
		attempt.Error = errorMessage
		attempt.Failure = failure
		attempt.FinishedAt = time.Now().UTC()
		if err = finishRecord(ctx, conn, attempt, message); err != nil {
			return err
		}
		finished = attempt
		return nil
	})
	if err != nil {
		return core.Attempt{}, err
	}
	return finished, nil
}

func sameFailureClass(a, b *core.FailureClass) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func finishRecord(ctx context.Context, conn executor, attempt core.Attempt, message string) error {
	if err := saveAttempt(ctx, conn, attempt); err != nil {
		return err
	}
	if _, err := insertEvent(ctx, conn, core.Event{TaskID: attempt.TaskID, AttemptID: attempt.ID, Type: "terminal_result", State: attempt.State, Text: message}); err != nil {
		return err
	}
	if _, err := insertEvent(ctx, conn, core.Event{TaskID: attempt.TaskID, AttemptID: attempt.ID, Type: "result_persisted", State: attempt.State}); err != nil {
		return err
	}
	if attempt.State != "unknown" {
		if _, err := conn.ExecContext(ctx, "DELETE FROM leases WHERE attempt_id=?", attempt.ID); err != nil {
			return err
		}
	}
	task, err := loadTask(ctx, conn, attempt.TaskID)
	if err != nil {
		return err
	}
	task.Status = attempt.State
	if task.Revision > attempt.Revision && attempt.State != "unknown" {
		task.Status = "draft"
	}
	task.UpdatedAt = time.Now().UTC()
	return saveTask(ctx, conn, task, false)
}

// Reconcile changes no owner, creates no attempt and never retries inference.
// confirmedStopped is external evidence supplied by the caller, not elapsed time.
func (s *Store) Reconcile(ctx context.Context, attemptID, expectedOwnerID string, confirmedStopped bool) (core.Attempt, error) {
	var reconciled core.Attempt
	err := s.write(ctx, func(conn executor) error {
		attempt, err := loadAttempt(ctx, conn, attemptID)
		if err != nil {
			return err
		}
		if err = ownerCheck(attempt, expectedOwnerID); err != nil {
			return err
		}
		if attempt.State != "running" && attempt.State != "unknown" {
			return problem("invalid_state", "Only running or uncertain work can be reconciled.")
		}
		if !confirmedStopped && attempt.State == "unknown" {
			reconciled = attempt
			return nil
		}
		if confirmedStopped {
			attempt.State = "interrupted"
			attempt.Error = "Stopped work was explicitly confirmed during reconciliation."
		} else {
			attempt.State = "unknown"
			attempt.Error = "The previous outcome is uncertain; ownership and workspace remain reserved."
		}
		attempt.FinishedAt = time.Now().UTC()
		if err = finishRecord(ctx, conn, attempt, attempt.Error); err != nil {
			return err
		}
		reconciled = attempt
		return nil
	})
	if err != nil {
		return core.Attempt{}, err
	}
	return reconciled, nil
}

// Completion messages are informational event data, not Attempt.Error. Schema v1
// has no event-type column, so this rare idempotent replay scans the attempt's
// indexed event bodies; a future typed index can remove the O(events) ceiling.
func completionMessage(ctx context.Context, conn executor, attempt core.Attempt) (string, error) {
	var raw sql.NullString
	err := conn.QueryRowContext(ctx, "SELECT CASE WHEN length(CAST(body AS BLOB))<=? THEN body END FROM events WHERE attempt_id=? AND json_extract(body,'$.type')='terminal_result' ORDER BY sequence DESC LIMIT 1", maxHistoryBytes, attempt.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", problem("state_corrupt", "A completed attempt is missing its terminal event.")
	}
	if err != nil {
		return "", err
	}
	if !raw.Valid {
		return "", historyTooLarge()
	}
	var event core.Event
	if err = decode(raw.String, &event); err != nil {
		return "", err
	}
	if event.TaskID != attempt.TaskID || event.AttemptID != attempt.ID || event.State != "complete" || event.Type != "terminal_result" || !validText(event.Text, maxText) {
		return "", problem("state_corrupt", "The completion event does not match its attempt.")
	}
	return event.Text, nil
}
