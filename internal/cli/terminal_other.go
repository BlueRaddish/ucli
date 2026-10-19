//go:build !windows && !linux && !darwin

package cli

// Unverified hosts retain all scripted paths without assuming terminal behavior.
func isTerminal(value any) bool { return false }
