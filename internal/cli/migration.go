package cli

import (
	"context"
	"fmt"

	"ucli.local/ucli/internal/app"
)

func migrate(ctx context.Context, a *app.App, destination string, o *output) int {
	receipt, err := a.Migrate(ctx, destination)
	// A verified backup may have been published before a later failure. Always
	// return its identity and migration status alongside the error, never lose
	// its recovery path or infer a successful rollback from a failed commit ACK.
	if o.json {
		_ = o.data("migration", receipt)
	} else {
		_ = o.text(fmt.Sprintf("Migration: %s | schema %d → %d\n", receipt.Status, receipt.FromSchema, receipt.ToSchema))
		if receipt.Backup.Path != "" {
			_ = o.text(fmt.Sprintf("Pre-migration backup: %s\nSHA-256: %s\n", receipt.Backup.Path, receipt.Backup.SHA256))
		}
		if receipt.ReceiptPath != "" {
			_ = o.text("Backup certificate: " + receipt.ReceiptPath + "\nThe certificate describes the snapshot; it does not prove the migration committed.\n")
		}
		if receipt.Status == "unknown" {
			_ = o.text("Commit acknowledgment is uncertain. Inspect ucli doctor --json before deciding what to do next.\n")
		}
		if receipt.Status == "applied" {
			_ = o.text("Task search metadata is enabled. Older schema-1-only UCLI builds will refuse this home.\n")
		}
		if receipt.Status == "unchanged" {
			_ = o.text("This home is already current. No backup was created.\n")
		}
		for _, warning := range receipt.Warnings {
			_ = o.progress("Migration warning: " + warning + "\n")
		}
	}
	if err != nil {
		return o.problem(err)
	}
	return 0
}
