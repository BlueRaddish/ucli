package state

import (
	"context"
	"time"
	"ucli.local/ucli/internal/core"
)

func insertEvent(ctx context.Context, conn executor, event core.Event) (core.Event, error) {
	if err := conn.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0)+1 FROM events WHERE attempt_id=?", event.AttemptID).Scan(&event.Sequence); err != nil {
		return core.Event{}, err
	}
	event.Version = 1
	event.At = time.Now().UTC()
	raw, err := encode(event)
	if err != nil {
		return core.Event{}, err
	}
	_, err = conn.ExecContext(ctx, "INSERT INTO events(attempt_id,sequence,body) VALUES(?,?,?)", event.AttemptID, event.Sequence, raw)
	return event, err
}

// AppendEvent assigns sequence/time; lifecycle mutations use Accept/Finish instead.
func (s *Store) AppendEvent(ctx context.Context, event core.Event, ownerID string) (core.Event, error) {
	allowed := map[string]bool{"status": true, "message_delta": true, "tool_activity": true, "input_required": true, "warning": true, "error": true}
	if !allowed[event.Type] || !validText(event.Text, maxText) {
		return core.Event{}, problem("invalid_input", "The event type is reserved/unknown or text exceeds 64 KiB.")
	}
	if event.State != "" && event.State != "running" {
		return core.Event{}, problem("invalid_input", "Only Finish or Reconcile can set a terminal state.")
	}
	var saved core.Event
	err := s.write(ctx, func(conn executor) error {
		attempt, err := loadAttempt(ctx, conn, event.AttemptID)
		if err != nil {
			return err
		}
		if err = ownerCheck(attempt, ownerID); err != nil {
			return err
		}
		if attempt.State != "running" {
			return problem("invalid_state", "A terminal or uncertain attempt cannot accept additional events.")
		}
		if event.TaskID != "" && event.TaskID != attempt.TaskID {
			return problem("invalid_input", "The event task does not match its attempt.")
		}
		event.TaskID = attempt.TaskID
		if event.Type == "warning" {
			bytes := len(event.Text)
			for _, warning := range attempt.Warnings {
				bytes += len(warning)
			}
			if len(attempt.Warnings) >= 16 || bytes > maxText {
				return problem("invalid_input", "Attempt warnings exceed 16 entries or 64 KiB in total.")
			}
			attempt.Warnings = append(attempt.Warnings, event.Text)
			if err = saveAttempt(ctx, conn, attempt); err != nil {
				return err
			}
		}
		saved, err = insertEvent(ctx, conn, event)
		return err
	})
	if err != nil {
		return core.Event{}, err
	}
	return saved, nil
}

func (s *Store) Events(ctx context.Context, attemptID string, afterSeq int64, limit int) ([]core.Event, error) {
	if afterSeq < 0 || limit < 1 || limit > 1000 {
		return nil, problem("invalid_input", "Event pagination requires limit 1–1000 and nonnegative sequence.")
	}
	events := []core.Event{}
	err := s.read(ctx, func(conn executor) error {
		if _, err := loadAttempt(ctx, conn, attemptID); err != nil {
			return err
		}
		rows, err := boundedRows(ctx, conn, "SELECT body FROM events WHERE attempt_id=? AND sequence>? ORDER BY sequence LIMIT ?", attemptID, afterSeq, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var event core.Event
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			if err = decode(raw, &event); err != nil {
				return err
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	return events, err
}
