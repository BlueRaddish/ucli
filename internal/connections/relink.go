package connections

import (
	"context"
	"time"
)

// Only an explicit Connect may replace an execution fingerprint. Status keeps
// its original pin even when discovery from newer bytes succeeds. Call under
// the metadata mutation lock; a failed inspection must append nothing.
func (s *Service) relinkReviewedExecutable(ctx context.Context, old Connection, sha string) (Connection, error) {
	mode := ""
	switch old.Provider {
	case "codex":
		mode = "chatgpt"
	case "claude":
		mode = "claude.ai"
	default:
		return old, problem("connection_changed", "This provider has no reviewed executable-relink path. Its existing account reference was kept.")
	}
	// Signed-out observations retain an old identity, but legitimately report
	// none/unknown auth. A genuinely unbound signed-out reference may acquire its
	// first identity here; an unknown or unexpected API mode is not a grant.
	restoring := old.AuthState == "signed-out" && (old.AuthMode == "none" || old.AuthMode == "unknown")
	if old.AuthMode != mode && !restoring || old.Identity == "" && !restoring {
		return old, problem("connection_changed", "The prior account binding cannot be safely restored by this native sign-in. Its existing reference was kept.")
	}
	if err := checkDirectory(old.NativeHome); err != nil {
		return old, err
	}
	candidate := old
	candidate.SHA256, candidate.Version = sha, ""
	candidate.AuthState, candidate.AuthMode, candidate.Identity = "unknown", "unknown", ""
	candidate.Compatibility, candidate.CatalogSHA256 = "unknown", ""
	candidate.Models, candidate.PlanType = nil, ""
	candidate.ObservedAt = time.Time{}
	updated, err := s.inspect(ctx, candidate)
	if err != nil {
		return old, err
	}
	if updated.ID != old.ID || updated.Label != old.Label || updated.Provider != old.Provider || updated.Executable != old.Executable || updated.NativeHome != old.NativeHome || updated.HomeMode != old.HomeMode || updated.SHA256 != sha || updated.Version == "" || updated.Compatibility != "reviewed" || updated.AuthState != "authenticated" || updated.AuthMode != mode || !validHash(updated.Identity) || old.Identity != "" && updated.Identity != old.Identity || updated.CatalogSHA256 != "" && updated.CatalogSHA256 != sha {
		return old, problem("connection_changed", "The updated executable did not verify the same native account and reviewed compatibility. Its existing reference and cached models were kept.")
	}
	// A native updater may race the status/catalog commands. Do not publish a
	// newly reviewed binding if the executable changed again during inspection.
	if err := verifyExecutable(ctx, updated); err != nil {
		return old, err
	}
	updated.ObservedAt = time.Now().UTC()
	updated.ManagedAI, updated.Isolation = false, "unverified"
	if err := s.append(ctx, updated, false); err != nil {
		return old, err
	}
	return updated, nil
}
