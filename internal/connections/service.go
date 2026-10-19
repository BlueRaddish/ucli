package connections

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ucli.local/ucli/internal/core"
)

func (s *Service) List(ctx context.Context) ([]Connection, error) {
	records, err := s.records(ctx)
	if err != nil {
		return nil, err
	}
	result := []Connection{}
	for _, r := range records {
		if !r.Removed {
			result = append(result, r.Connection)
		}
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, idOrLabel string) (Connection, error) {
	if !validText(idOrLabel, 128) || idOrLabel == "" {
		return Connection{}, problem("connection_input", "Choose a connection ID or exact label.")
	}
	items, err := s.List(ctx)
	if err != nil {
		return Connection{}, err
	}
	for _, c := range items {
		if c.ID == idOrLabel || c.Label == idOrLabel {
			return c, nil
		}
	}
	return Connection{}, problem("connection_not_found", "No cached native connection matches that ID or label. Add a connection first.")
}
func (s *Service) Connect(ctx context.Context, o ConnectOptions) (Connection, error) {
	labelExplicit := o.Label != ""
	if !providerKnown(o.Provider) {
		return Connection{}, problem("connection_input", "Choose codex, claude, agy or github.")
	}
	if o.HomeMode == "" {
		o.HomeMode = "existing"
	}
	if o.HomeMode != "existing" && o.HomeMode != "separate" {
		return Connection{}, problem("connection_input", "Native home mode must be existing or separate.")
	}
	if o.Label == "" {
		o.Label = o.Provider
	}
	if !validText(o.Label, 128) || strings.TrimSpace(o.Label) == "" || validID(o.Label) {
		return Connection{}, problem("connection_input", "Connection label must be nonempty single-line text, at most 128 bytes, and cannot use connection-ID syntax.")
	}
	if o.Executable == "" {
		o.Executable = findExecutable(o.Provider)
	}
	sha, err := executableHash(ctx, o.Executable)
	if err != nil {
		return Connection{}, err
	}
	if o.NativeHome == "" {
		if o.HomeMode == "separate" {
			o.NativeHome = join(s.home, "native", o.Provider, core.NewID("profile"))
		} else {
			o.NativeHome = defaultHome(o.Provider)
		}
	}
	home, err := physicalPath(o.NativeHome)
	if err != nil {
		return Connection{}, err
	}
	if o.HomeMode == "separate" && !within(join(s.home, "native", o.Provider), home) {
		return Connection{}, problem("connection_input", "Separate native homes must be under this UCLI home's native/provider directory.")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer unlock()
	items, err := s.List(ctx)
	if err != nil {
		return Connection{}, err
	}
	for _, c := range items {
		if c.Provider == o.Provider && samePath(c.NativeHome, home) {
			if !labelExplicit {
				o.Label = c.Label
			}
			if c.Label != o.Label || !samePath(c.Executable, o.Executable) || c.SHA256 != sha || c.HomeMode != o.HomeMode {
				return Connection{}, problem("connection_conflict", "This native home is already connected with different metadata. Existing references were kept.")
			}
			return s.observe(ctx, c)
		}
	}
	for _, c := range items {
		if c.Label == o.Label {
			return Connection{}, problem("connection_conflict", "That connection label is already in use. Choose another label.")
		}
	}
	c := Connection{ID: core.NewID("connection"), Label: o.Label, Provider: o.Provider, Executable: filepath.Clean(o.Executable), SHA256: sha, NativeHome: home, HomeMode: o.HomeMode, AuthState: "unknown", AuthMode: "unknown", Isolation: "unverified"}
	return s.observe(ctx, c)
}
func (s *Service) Status(ctx context.Context, idOrLabel string) (Connection, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer unlock()
	c, err := s.Get(ctx, idOrLabel)
	if err != nil {
		return c, err
	}
	return s.observe(ctx, c)
}
func (s *Service) observe(ctx context.Context, c Connection) (Connection, error) {
	if err := verifyExecutable(ctx, c); err != nil {
		return c, err
	}
	if _, err := os.Lstat(c.NativeHome); os.IsNotExist(err) {
		// An absent native directory does not rule out shared OS credentials.
		// Only the explicit separate Codex file-auth envelope has no such store.
		c.AuthState, c.AuthMode = "unknown", "unknown"
		if c.HomeMode == "separate" && c.Provider == "codex" {
			c.AuthState, c.AuthMode = "signed-out", "none"
		}
		c.Models = nil
	} else if err != nil {
		return c, problem("connection_home", "Native home could not be inspected; it was left untouched.")
	} else {
		if err = checkDirectory(c.NativeHome); err != nil {
			return c, err
		}
		updated, err := s.inspect(ctx, c)
		if err != nil {
			return c, err
		}
		// Logging out or an unavailable identity must not erase the old account
		// pin and permit another account to inherit the same connection ID later.
		if updated.Identity == "" {
			updated.Identity = c.Identity
		}
		// An observed identity change must never silently rebind an old label.
		if c.Identity != "" && updated.Identity != "" && c.Identity != updated.Identity {
			c.AuthState, c.Models = "identity-changed", nil
			c.ObservedAt = time.Now().UTC()
			if err = s.append(ctx, c, false); err != nil {
				return c, err
			}
			return c, problem("connection_changed", "Native account identity changed. Remove this local reference and add the intended account explicitly; no credentials were changed.")
		}
		c = updated
	}
	c.ObservedAt = time.Now().UTC()
	c.ManagedAI = false
	c.Isolation = "unverified"
	if err := s.append(ctx, c, false); err != nil {
		return c, err
	}
	return c, nil
}

// Remove forgets local metadata only. Native logout, revocation and credential
// deletion are intentionally absent, including for UCLI-created native homes.
func (s *Service) Remove(ctx context.Context, idOrLabel string) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	c, err := s.Get(ctx, idOrLabel)
	if err != nil {
		return err
	}
	return s.append(ctx, c, true)
}
func (s *Service) Login(ctx context.Context, idOrLabel string, emit func(LoginEvent) error) (Connection, error) {
	if emit == nil {
		return Connection{}, problem("connection_input", "Native login needs an ephemeral event consumer.")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer unlock()
	c, err := s.Get(ctx, idOrLabel)
	if err != nil {
		return c, err
	}
	c, err = s.observe(ctx, c)
	if err != nil {
		return c, err
	}
	if c.AuthState == "authenticated" {
		return c, emit(LoginEvent{Type: "complete", Message: "Existing native sign-in is connected; no login or account switch was performed."})
	}
	if c.AuthState != "signed-out" {
		return c, problem("connection_login_unavailable", "Native sign-in status is unknown. Use the provider's native sign-in and refresh this connection; no active account was replaced.")
	}
	if c.Provider == "agy" || c.HomeMode == "separate" && c.Provider != "codex" {
		return c, problem("connection_login_unavailable", "This provider's separate credential/keyring scope is unverified. Complete native sign-in separately, then connect its existing home.")
	}
	if c.HomeMode == "separate" {
		if err = privateDirs(c.NativeHome); err != nil {
			return c, err
		}
	}
	args, err := loginArgs(c)
	if err != nil {
		return c, err
	}
	if err = emit(LoginEvent{Type: "starting", Message: "Starting the selected native sign-in. URLs and device codes are temporary; do not save or share them."}); err != nil {
		return c, err
	}
	_, err = run(ctx, c, args, 10*time.Minute, func(line string) error { return loginLine(c.Provider, line, emit) })
	if err != nil {
		return c, err
	}
	c, err = s.observe(ctx, c)
	if err != nil {
		return c, err
	}
	if c.AuthState != "authenticated" {
		return c, problem("connection_login_incomplete", "Native login ended without a verified signed-in result. Refresh status after completing the provider flow.")
	}
	return c, emit(LoginEvent{Type: "complete", Message: "Native sign-in connected. This does not enable managed AI or hosted UCLI synchronization."})
}
func onlyContext(err error) bool {
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if e, ok := err.(interface{ Unwrap() []error }); ok {
		for _, v := range e.Unwrap() {
			if !onlyContext(v) {
				return false
			}
		}
		return true
	}
	if e, ok := err.(interface{ Unwrap() error }); ok {
		return onlyContext(e.Unwrap())
	}
	return false
}
