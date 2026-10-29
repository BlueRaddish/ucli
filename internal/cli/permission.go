package cli

import (
	"context"
	"fmt"

	"ucli.local/ucli/internal/app"
)

func permissionChoices(demo bool) []choice {
	var rows []choice
	for _, mode := range app.PermissionModes(demo) {
		label := "Read-only - text input only; native tools are disabled"
		if demo {
			label = "Read-only - local simulation or echo; no workspace edits"
		} else if mode == "sandbox" {
			label = "Sandbox - full local computer access through native tools; no OS isolation"
		}
		rows = append(rows, choice{mode, label})
	}
	return rows
}

func permissionSupported(mode string, demo bool) bool {
	for _, supported := range app.PermissionModes(demo) {
		if mode == supported {
			return true
		}
	}
	return false
}

func (s *session) selectPermission(ctx context.Context) error {
	current := s.selection.WithDefaults().Permission
	rows := permissionChoices(s.demo)
	title := "Choose permission for the next submission"
	if !permissionSupported(current, s.demo) {
		title = fmt.Sprintf("Permission %s is unsupported here - choose a mode or go back unchanged", current)
	}
	value, err := s.choose(ctx, title, rows, current)
	if err != nil || value == "" || value == current {
		return err
	}
	previous := s.selection
	s.selection.Permission = value
	if s.task != nil {
		if err := s.storeDraft(ctx, s.task.Draft, false); err != nil {
			s.selection = previous
			return err
		}
	}
	message := "Read-only selected: native tools are disabled; send your text when ready."
	if s.demo {
		message = "Read-only selected: local simulation or echo cannot edit your workspace."
	} else if value == "sandbox" {
		message = "Sandbox selected: full local computer access through native tools; no OS isolation. Explicit choice saved."
	}
	if err := s.out.text(message + "\n"); err != nil {
		return err
	}
	return s.out.notice("Permission selected: "+value+" | prompt kept; Enter sends", toneSuccess)
}
