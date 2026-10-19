package app

import (
	"context"
	"path/filepath"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

// Migrate is explicit and local. Opening a home never advances its schema.
func (a *App) Migrate(ctx context.Context, destination string) (state.MigrationReceipt, error) {
	if destination == "" {
		return state.MigrationReceipt{}, core.Problem("invalid_input", "A new pre-migration backup filename is required.", 2)
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return state.MigrationReceipt{}, core.Problem("invalid_input", "The backup filename could not be resolved.", 2)
	}
	return a.Store.Migrate(ctx, abs)
}
