package cli

import (
	"context"
	"errors"
	"fmt"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func backup(ctx context.Context, a *app.App, destination string, o *output) int {
	receipt, err := a.Backup(ctx, destination)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			err = core.Problem("interrupted", "Backup was interrupted; no completed backup receipt was returned. Check the destination before retrying.", 130)
		case errors.Is(err, context.DeadlineExceeded):
			err = core.Problem("deadline", "Backup timed out; no completed backup receipt was returned. Check the destination before retrying.", 124)
		}
		return o.problem(err)
	}
	if o.json {
		_ = o.data("backup", receipt)
		return 0
	}
	_ = o.text(fmt.Sprintf("Backup saved: %s\nSHA-256: %s\nBytes: %d | schema: %d\nTasks: %d | attempts: %d | events: %d | unsettled attempts: %d\n", receipt.Path, receipt.SHA256, receipt.Bytes, receipt.Schema, receipt.Tasks, receipt.Attempts, receipt.Events, receipt.UnsettledAttempts))
	_ = o.text("Includes saved prompts, results and drafts. Native credential stores are not included.\nUnsettled attempts remain unsettled. No work was restored or resumed.\n")
	for _, warning := range receipt.Warnings {
		_ = o.progress("Backup warning: " + warning + "\n")
	}
	return 0
}
