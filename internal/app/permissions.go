package app

import (
	"fmt"
	"strings"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/routing"
)

// PermissionModes lists only envelopes implemented by the current execution
// paths. It is picker metadata, not evidence that a particular native binding
// is admitted; drivers still verify their own configuration before dispatch.
func PermissionModes(demo bool) []string {
	if demo {
		return []string{"read-only"}
	}
	return []string{"read-only", "sandbox"}
}

func permissionCapabilities(demo bool) map[string]map[string]routing.Capability {
	filesystem := map[string]routing.Capability{}
	for _, mode := range PermissionModes(demo) {
		filesystem[mode] = routing.Capability{Support: "mapped"}
	}
	return map[string]map[string]routing.Capability{"filesystem": filesystem}
}

func requirePermission(selection core.Selection, demo bool) error {
	mode := selection.WithDefaults().Permission
	for _, supported := range PermissionModes(demo) {
		if mode == supported {
			return nil
		}
	}
	runtime, choices := "Codex and Claude Code", "read-only (text only, no tools) or sandbox (full local computer access)"
	if demo {
		runtime, choices = "local demonstrations and Muse echo", "read-only (no workspace edits)"
	} else if strings.HasPrefix(selection.Model, "claude/") {
		runtime = "Claude Code"
	} else if strings.HasPrefix(selection.Model, "codex/") {
		runtime = "Codex"
	}
	err := core.Problem("unsupported_permission", fmt.Sprintf("Permission %q is not supported by %s in UCLI. Open /permission and explicitly choose %s, then send again. For command-line use, set --permission. No prompt was sent and your permission was not changed.", mode, runtime, choices), 5)
	err.Acceptance, err.SideEffects = "not-dispatched", "none"
	return err
}
