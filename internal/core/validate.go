package core

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxInputBytes = 1024 * 1024
const MaxOutputBytes = 4 * 1024 * 1024

// ValidateFailureClass is shared by terminal writes and receipt replay reads.
// Nil is the historical unclassified failure. Callers separately require failed
// attempt state: metadata alone never settles unknown or interrupted work.
func ValidateFailureClass(failure *FailureClass) error {
	if failure == nil {
		return nil
	}
	invalid := func() error {
		return Problem("invalid_failure", "Failure classification contains an unsupported exit or invalid metadata.", 2)
	}
	switch failure.Exit {
	case 2, 3, 4, 5, 6, 8, 9:
	default:
		return invalid()
	}
	for _, value := range []string{failure.Code, failure.Origin} {
		if len(value) == 0 || len(value) > 128 {
			return invalid()
		}
		for _, r := range value {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.-", r)) {
				return invalid()
			}
		}
	}
	for _, value := range []string{failure.Acceptance, failure.SideEffects} {
		if len(value) > 64 || !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
			return invalid()
		}
		for _, r := range value {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				return invalid()
			}
		}
	}
	return nil
}

func ValidateSelection(s Selection) error {
	s = s.WithDefaults()
	if !oneOf(s.Effort, "auto", "low", "medium", "high", "max") {
		return Problem("invalid_effort", "Choose effort auto, low, medium, high or max.", 2)
	}
	if !oneOf(s.Permission, "read-only", "workspace-write", "unrestricted", "sandbox") {
		return Problem("invalid_permission", "Choose permission read-only, workspace-write, unrestricted or sandbox (full local computer access).", 2)
	}
	for _, v := range []string{s.Model, s.Account} {
		if len(v) > 256 || strings.ContainsAny(v, "\x00\r\n") || !utf8.ValidString(v) {
			return Problem("invalid_selection", "Model/account identifiers must be valid single-line text under 257 bytes.", 2)
		}
	}
	return nil
}
func ValidateInput(input string) error {
	if strings.TrimSpace(input) == "" {
		return Problem("empty_input", "Enter a task to run.", 2)
	}
	if len(input) > MaxInputBytes || !utf8.ValidString(input) || strings.ContainsRune(input, 0) {
		return Problem("invalid_input", "Task input must be valid UTF-8, without NUL, and at most 1 MiB.", 2)
	}
	return nil
}
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
