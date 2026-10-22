package connections

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestExplicitConnectRelinksReviewedUpdateWithoutChangingAccount(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			s, o, _ := fixture(t)
			o.Provider = provider
			mode := "chatgpt"
			if provider == "claude" {
				mode = "claude.ai"
			}
			initial := s.inspect
			s.inspect = func(ctx context.Context, c Connection) (Connection, error) {
				c, err := initial(ctx, c)
				c.AuthMode = mode
				return c, err
			}
			old, err := s.Connect(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			before := recordBytes(t, s)
			sha := updateFixtureExecutable(t, o)
			s.inspect = func(_ context.Context, c Connection) (Connection, error) {
				if c.ID != old.ID || c.SHA256 != sha || c.Version != "" || c.Identity != "" || c.AuthMode != "unknown" || c.Compatibility != "unknown" || c.Models != nil || c.CatalogSHA256 != "" {
					t.Fatal("relink did not request fresh evidence with the same reference")
				}
				return reviewedRelink(c, old.Identity, mode), nil
			}
			o.Label = "" // Reuse preserves the existing custom label.
			got, err := s.Connect(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != old.ID || got.Label != old.Label || got.Identity != old.Identity || got.NativeHome != old.NativeHome || got.Executable != old.Executable || got.HomeMode != old.HomeMode || got.SHA256 != sha || got.Version != "2.0.0" || got.Compatibility != "reviewed" || got.CatalogSHA256 != sha || len(got.Models) != 1 || got.Models[0].ID != "fresh" {
				t.Fatal("relink lost the stable binding or new evidence")
			}
			after := recordBytes(t, s)
			if len(after) != len(before)+1 {
				t.Fatal("relink did not append exactly one new receipt")
			}
			for name, raw := range before {
				if after[name] != raw {
					t.Fatal("relink rewrote old evidence")
				}
			}
			reopened, err := New(s.home)
			if err != nil {
				t.Fatal(err)
			}
			cached, err := reopened.Get(context.Background(), old.ID)
			if err != nil || !reflect.DeepEqual(cached, got) {
				t.Fatal("relink did not survive offline restart", err)
			}
		})
	}
}

func TestExplicitRelinkRefusalsPreserveEveryPriorRecord(t *testing.T) {
	for _, test := range []struct {
		name         string
		mutate       func(*Connection)
		inspectError bool
		changeFile   bool
	}{
		{name: "different-account", mutate: func(c *Connection) { c.Identity = IdentityFingerprint(c.Provider, "other") }},
		{name: "empty-account", mutate: func(c *Connection) { c.Identity = "" }},
		{name: "unknown-build", mutate: func(c *Connection) { c.Compatibility = "unknown" }},
		{name: "missing-review", mutate: func(c *Connection) { c.Compatibility = "" }},
		{name: "wrong-auth-mode", mutate: func(c *Connection) { c.AuthMode = "api-key" }},
		{name: "signed-out", mutate: func(c *Connection) { c.AuthState = "signed-out" }},
		{name: "stale-catalog", mutate: func(c *Connection) { c.CatalogSHA256 = IdentityFingerprint("fixture", "stale") }},
		{name: "wrong-home", mutate: func(c *Connection) { c.NativeHome += "-other" }},
		{name: "inspect-failed", inspectError: true},
		{name: "updater-race", changeFile: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, o, _ := fixture(t)
			old, err := s.Connect(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			before := recordBytes(t, s)
			updateFixtureExecutable(t, o)
			s.inspect = func(_ context.Context, c Connection) (Connection, error) {
				c = reviewedRelink(c, old.Identity, "chatgpt")
				if test.mutate != nil {
					test.mutate(&c)
				}
				if test.changeFile {
					if err := os.WriteFile(o.Executable, []byte("second concurrent update"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if test.inspectError {
					return c, problem("connection_catalog", "Synthetic unavailable catalog.")
				}
				return c, nil
			}
			got, err := s.Connect(context.Background(), o)
			if err == nil || !reflect.DeepEqual(got, old) || !reflect.DeepEqual(recordBytes(t, s), before) {
				t.Fatal("failed relink changed the cached reference or its evidence")
			}
		})
	}
}

func TestExplicitRelinkAllowsOnlyExpectedRestoredOrFirstIdentity(t *testing.T) {
	for _, test := range []struct {
		name, oldState, oldMode string
		bound, accepted         bool
	}{
		{"first-signed-out", "signed-out", "none", false, true},
		{"first-unknown-mode-signed-out", "signed-out", "unknown", false, true},
		{"restored-signed-out", "signed-out", "none", true, true},
		{"restored-unknown-mode-signed-out", "signed-out", "unknown", true, true},
		{"unbound-unknown-status", "unknown", "unknown", false, false},
		{"bound-unknown-status", "unknown", "unknown", true, false},
		{"previous-api-mode", "signed-out", "api-key", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, o, _ := fixture(t)
			identity := IdentityFingerprint(o.Provider, "expected-account")
			s.inspect = func(_ context.Context, c Connection) (Connection, error) {
				c.AuthState, c.AuthMode = test.oldState, test.oldMode
				if test.bound {
					c.Identity = identity
				}
				return c, nil
			}
			old, err := s.Connect(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			before := recordBytes(t, s)
			updateFixtureExecutable(t, o)
			s.inspect = func(_ context.Context, c Connection) (Connection, error) {
				c = reviewedRelink(c, identity, "chatgpt")
				c.CatalogSHA256 = "" // Optional evidence cannot become a new mandatory inspector contract.
				return c, nil
			}
			got, err := s.Connect(context.Background(), o)
			if test.accepted {
				if err != nil || got.ID != old.ID || got.Identity != identity || got.AuthState != "authenticated" || got.SHA256 == old.SHA256 {
					t.Fatal("explicit restored/first binding refused", err)
				}
			} else if err == nil || !reflect.DeepEqual(got, old) || !reflect.DeepEqual(recordBytes(t, s), before) {
				t.Fatal("unverified prior auth mode was upgraded")
			}
		})
	}
}

func reviewedRelink(c Connection, identity, mode string) Connection {
	c.AuthState, c.AuthMode, c.Identity = "authenticated", mode, identity
	c.Version, c.Compatibility, c.CatalogSHA256 = "2.0.0", "reviewed", c.SHA256
	c.Models = []Model{{ID: "fresh", Name: "Fresh", EffortState: "supported", Efforts: []string{"low"}}}
	return c
}

func updateFixtureExecutable(t *testing.T, o ConnectOptions) string {
	t.Helper()
	if err := os.WriteFile(o.Executable, []byte("updated reviewed synthetic executable"), 0700); err != nil {
		t.Fatal(err)
	}
	sha, err := executableHash(context.Background(), o.Executable)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func recordBytes(t *testing.T, s *Service) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		raw, err := os.ReadFile(join(s.directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = string(raw)
	}
	return result
}
