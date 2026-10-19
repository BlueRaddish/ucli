package state

import (
	"context"
	"strings"

	"ucli.local/ucli/internal/core"
)

// RecordNativeContext saves provenance before a native turn is dispatched. It
// neither authorizes native resume nor establishes any external task outcome.
// An identical replay returns the original event; context cannot be replaced.
func (s *Store) RecordNativeContext(ctx context.Context, attemptID, ownerID string, native core.NativeContext) (core.Event, error) {
	if !validID(attemptID) || !validID(native.Runtime) || !validID(native.Provider) || !validID(native.Account) || !validID(native.SessionID) || strings.TrimSpace(native.SessionID) == "" || (native.Durability != "ephemeral" && native.Durability != "durable") {
		return core.Event{}, problem("invalid_input", "Native context requires bounded namespace/session IDs and ephemeral or durable storage.")
	}
	if native.Provenance != "" && native.Provenance != "allocated" && native.Provenance != "confirmed" {
		return core.Event{}, problem("invalid_input", "Native context provenance must be allocated or confirmed.")
	}
	var saved core.Event
	err := s.write(ctx, func(conn executor) error {
		attempt, err := loadAttempt(ctx, conn, attemptID)
		if err != nil {
			return err
		}
		if err = ownerCheck(attempt, ownerID); err != nil {
			return err
		}
		if attempt.State != "running" {
			return problem("invalid_state", "Only a running owned attempt can record native context.")
		}
		route := attempt.Effective
		if native.Runtime != route.Runtime || native.Provider != route.Provider || native.Account != route.Account {
			return problem("native_context_conflict", "Native context does not match the accepted runtime, provider and account namespace.")
		}
		if attempt.NativeContext != nil {
			if *attempt.NativeContext != native {
				return problem("native_context_conflict", "An attempt's recorded native context cannot be replaced.")
			}
			saved, err = contextEvent(ctx, conn, attempt, native)
			return err
		}
		raw, err := encode(native)
		if err != nil {
			return err
		}
		attempt.NativeContext = &native
		if err = saveAttempt(ctx, conn, attempt); err != nil {
			return err
		}
		saved, err = insertEvent(ctx, conn, core.Event{TaskID: attempt.TaskID, AttemptID: attempt.ID, Type: "runtime_context", State: "running", Text: raw})
		return err
	})
	if err != nil {
		return core.Event{}, err
	}
	return saved, nil
}

// Schema v1 stores event types inside JSON. Replay scans the attempt's event
// index without loading unrelated bodies. A future typed event index can remove
// this O(events) scan when long histories justify a schema migration.
func contextEvent(ctx context.Context, conn executor, attempt core.Attempt, native core.NativeContext) (core.Event, error) {
	rows, err := boundedRows(ctx, conn, "SELECT body FROM events WHERE attempt_id=? AND json_extract(body,'$.type')='runtime_context' ORDER BY sequence LIMIT 2", attempt.ID)
	if err != nil {
		return core.Event{}, err
	}
	defer rows.Close()
	var event core.Event
	count := 0
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return core.Event{}, err
		}
		if err = decode(raw, &event); err != nil {
			return core.Event{}, err
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return core.Event{}, err
	}
	if count != 1 || event.TaskID != attempt.TaskID || event.AttemptID != attempt.ID || event.Type != "runtime_context" || event.State != "running" {
		return core.Event{}, problem("state_corrupt", "Native context is missing its unique matching durable event.")
	}
	var stored core.NativeContext
	if err = decode(event.Text, &stored); err != nil {
		return core.Event{}, err
	}
	if stored != native {
		return core.Event{}, problem("state_corrupt", "Native context and its durable event disagree.")
	}
	return event, nil
}
