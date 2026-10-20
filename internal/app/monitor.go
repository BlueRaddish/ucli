package app

import (
	"context"

	"ucli.local/ucli/internal/core"
)

// AttemptEvents reads durable observations, including output saved before an
// attempt finishes. Store owns ID, sequence, page-size and schema validation.
// Reading an event does not establish that its executor is still alive.
func (a *App) AttemptEvents(ctx context.Context, attemptID string, afterSeq int64, limit int) ([]core.Event, error) {
	return a.Store.Events(ctx, attemptID, afterSeq, limit)
}
