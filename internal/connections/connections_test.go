package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

func fixture(t *testing.T) (*Service, ConnectOptions, *int) {
	t.Helper()
	root := t.TempDir()
	s, err := New(join(root, "ucli"))
	if err != nil {
		t.Fatal(err)
	}
	exe := join(root, "provider.exe")
	if err = os.WriteFile(exe, []byte("synthetic executable bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	native := join(root, "native")
	if err = os.Mkdir(native, 0700); err != nil {
		t.Fatal(err)
	}
	calls := new(int)
	s.inspect = func(_ context.Context, c Connection) (Connection, error) {
		*calls++
		c.AuthState = "authenticated"
		c.AuthMode = "chatgpt"
		c.Version = "1.2.3"
		c.Identity = IdentityFingerprint(c.Provider, "synthetic-account")
		c.Models = []Model{{ID: "synthetic", Name: "Synthetic", Default: true, DefaultEffort: "medium", Efforts: []string{"low", "medium"}}}
		return c, nil
	}
	return s, ConnectOptions{Provider: "codex", Label: "personal", Executable: exe, NativeHome: native}, calls
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *core.Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func TestCachedListAndRemoveNeverProbeOrTouchNativeCredentials(t *testing.T) {
	s, o, calls := fixture(t)
	ctx := context.Background()
	if items, err := s.List(ctx); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	if _, err := os.Stat(s.home); !os.IsNotExist(err) {
		t.Fatal("passive list created home")
	}
	secret := join(o.NativeHome, "native-secret-sentinel")
	os.WriteFile(secret, []byte("PRIVATE-TOKEN-DO-NOT-COPY"), 0600)
	c, err := s.Connect(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || c.ManagedAI || c.Isolation != "unverified" || len(c.Models) != 1 {
		t.Fatal("wrong connection scope", c)
	}
	if got, err := s.Get(ctx, "personal"); err != nil || got.ID != c.ID {
		t.Fatal(got, err)
	}
	if _, err = s.List(ctx); err != nil || *calls != 1 {
		t.Fatal("list probed native")
	}
	if err = s.Remove(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatal("remove probed native")
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	if raw, err := os.ReadFile(secret); err != nil || string(raw) != "PRIVATE-TOKEN-DO-NOT-COPY" {
		t.Fatal("native secret changed")
	}
	entries, _ := os.ReadDir(s.directory)
	for _, entry := range entries {
		raw, _ := os.ReadFile(join(s.directory, entry.Name()))
		if strings.Contains(string(raw), "PRIVATE-TOKEN") {
			t.Fatal("credential copied")
		}
	}
	reopened, err := New(s.home)
	if err != nil {
		t.Fatal(err)
	}
	if items, err := reopened.List(ctx); err != nil || len(items) != 0 {
		t.Fatal("tombstone lost", items, err)
	}
}
func TestChangedAccountAndExecutableNeverSilentlyRebind(t *testing.T) {
	s, o, _ := fixture(t)
	ctx := context.Background()
	first, err := s.Connect(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	s.inspect = func(_ context.Context, c Connection) (Connection, error) {
		c.AuthState, c.AuthMode, c.Identity = "signed-out", "none", ""
		c.Models = nil
		return c, nil
	}
	signedOut, err := s.Status(ctx, first.ID)
	if err != nil || signedOut.Identity != first.Identity || signedOut.AuthState != "signed-out" {
		t.Fatal("sign-out erased account pin", err)
	}
	s.inspect = func(_ context.Context, c Connection) (Connection, error) {
		c.Identity = IdentityFingerprint(c.Provider, "different-account")
		return c, nil
	}
	changed, err := s.Status(ctx, first.ID)
	requireCode(t, err, "connection_changed")
	if changed.Identity != first.Identity || changed.AuthState != "identity-changed" || len(changed.Models) != 0 {
		t.Fatal("account pin overwritten")
	}
	cached, err := s.Get(ctx, first.ID)
	if err != nil || cached.AuthState != "identity-changed" {
		t.Fatal("change not persisted", err)
	}
	if err = os.WriteFile(o.Executable, []byte("different native binary"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = s.Status(ctx, first.ID)
	requireCode(t, err, "connection_changed")
}
func TestSignedOutRegistrationAndSeparateNativeHomeStayPassive(t *testing.T) {
	s, o, _ := fixture(t)
	o.Provider = "claude"
	o.HomeMode = "separate"
	o.NativeHome = ""
	c, err := s.Connect(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthState != "unknown" || !within(join(s.home, "native", "claude"), c.NativeHome) {
		t.Fatal(c)
	}
	if _, err = os.Stat(c.NativeHome); !os.IsNotExist(err) {
		t.Fatal("registration created native home")
	}
	_, err = s.Login(context.Background(), c.ID, func(LoginEvent) error { return nil })
	requireCode(t, err, "connection_login_unavailable")
	if _, err = os.Stat(c.NativeHome); !os.IsNotExist(err) {
		t.Fatal("unsupported login created native home")
	}
	o.NativeHome = t.TempDir()
	_, err = s.Connect(context.Background(), o)
	requireCode(t, err, "connection_input")
	o.Provider, o.Label, o.NativeHome = "codex", "separate-codex", ""
	c, err = s.Connect(context.Background(), o)
	if err != nil || c.AuthState != "signed-out" || c.AuthMode != "none" {
		t.Fatal("separate file-auth envelope lost", err)
	}
	o.HomeMode, o.Label, o.NativeHome = "existing", "missing-existing", join(t.TempDir(), "absent")
	c, err = s.Connect(context.Background(), o)
	if err != nil || c.AuthState != "unknown" {
		t.Fatal("missing home was assumed to lack OS credentials", err)
	}
}
func TestExistingAuthenticatedLoginDoesNotSwitchAccounts(t *testing.T) {
	s, o, _ := fixture(t)
	c, err := s.Connect(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	events := []LoginEvent{}
	got, err := s.Login(context.Background(), c.ID, func(event LoginEvent) error { events = append(events, event); return nil })
	if err != nil || got.Identity != c.Identity || len(events) != 1 || events[0].Type != "complete" {
		t.Fatal(got, events, err)
	}
	o.Label = ""
	reused, err := s.Connect(context.Background(), o)
	if err != nil || reused.ID != c.ID || reused.Label != "personal" {
		t.Fatal("omitted label lost existing connection", err)
	}
	other := o
	other.Label, other.NativeHome = "codex", t.TempDir()
	if _, err = s.Connect(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	// Native-home lookup takes priority over a different record using the
	// default provider label, independent of random connection ID sort order.
	if reused, err = s.Connect(context.Background(), o); err != nil || reused.ID != c.ID {
		t.Fatal("default label shadowed native-home reuse", err)
	}
}
func TestImmutableHistoryRejectsDamageAndForeignFiles(t *testing.T) {
	for _, mode := range []string{"checksum", "name", "unknown", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			s, o, _ := fixture(t)
			if _, err := s.Connect(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(s.directory)
			var path string
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".json") {
					path = join(s.directory, e.Name())
				}
			}
			before, _ := os.ReadFile(path)
			switch mode {
			case "checksum":
				os.WriteFile(path, []byte(strings.Replace(string(before), "personal", "tampered", 1)), 0600)
			case "name":
				os.Rename(path, join(s.directory, "foreign.json"))
			case "unknown":
				os.WriteFile(join(s.directory, "foreign.txt"), []byte("keep"), 0600)
			case "oversized":
				os.WriteFile(path, []byte(strings.Repeat("x", (64<<10)+1)), 0600)
			}
			if _, err := s.List(context.Background()); err == nil {
				t.Fatal("damaged history accepted")
			}
			if _, err := s.Connect(context.Background(), o); err == nil {
				t.Fatal("damaged history overwritten")
			}
		})
	}
}
func TestPublicationLimitLeavesExistingHistoryReadable(t *testing.T) {
	s, o, _ := fixture(t)
	ctx := context.Background()
	c, err := s.Connect(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	other := o
	other.Label, other.NativeHome = c.ID, t.TempDir()
	_, err = s.Connect(ctx, other)
	requireCode(t, err, "connection_input")
	// Capacity accounts for the full immutable history, not merely live IDs.
	entries, _ := os.ReadDir(s.directory)
	for i := len(entries); i < 1022; i++ {
		if err = os.WriteFile(join(s.directory, fmt.Sprintf(".pending-fixture-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Status(ctx, c.ID); err != nil {
		t.Fatal("last bounded publication failed", err)
	}
	_, err = s.Status(ctx, c.ID)
	requireCode(t, err, "connection_store")
	if got, err := s.Get(ctx, c.ID); err != nil || got.ID != c.ID {
		t.Fatal("capacity refusal made previous records unreadable", err)
	}
	// A synthetic size proposal exercises the aggregate guard without writing
	// megabytes of duplicate account metadata into the test fixture.
	otherStore, _, _ := fixture(t)
	if err = privateDirs(otherStore.directory); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(join(otherStore.directory, "prior.json"), []byte("size sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	requireCode(t, otherStore.publicationFits(ctx, 16<<20), "connection_store")
}
func TestSeparateServicesSerializeDuplicateRegistration(t *testing.T) {
	s, o, _ := fixture(t)
	const count = 4
	results := make(chan Connection, count)
	failures := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := New(s.home)
			if err != nil {
				failures <- err
				return
			}
			other.inspect = s.inspect
			c, err := other.Connect(context.Background(), o)
			results <- c
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for c := range results {
		if id != "" && id != c.ID {
			t.Fatal("duplicate native home registrations")
		}
		id = c.ID
	}
	items, err := s.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	unlock, err := s.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other, _ := New(s.home)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = other.Status(ctx, id)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock ignored context", err)
	}
	unlock()
	if _, err = other.Get(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
func TestLoginOutputWhitelistDoesNotExposeNativeDiagnostics(t *testing.T) {
	events := []LoginEvent{}
	emit := func(e LoginEvent) error { events = append(events, e); return nil }
	for _, line := range []string{"private email person@example.invalid and refresh_token=DO-NOT-EMIT", "https://evil.invalid/?token=DO-NOT-EMIT", "https://auth.openai.com.evil.invalid/device", "https://user@auth.openai.com/device", "First copy your one-time code: ABCD-1234", "https://auth.openai.com/codex/device"} {
		if err := loginLine("codex", line, emit); err != nil {
			t.Fatal(err)
		}
	}
	if err := loginLine("codex", "https://auth.openai.com/codex/device?access_token=DO-NOT-EMIT", emit); err != nil {
		t.Fatal(err)
	}
	if err := loginLine("claude", "https://claude.ai/oauth/authorize?access_token=DO-NOT-EMIT", emit); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Code != "ABCD-1234" || events[1].URL != "https://auth.openai.com/codex/device" {
		t.Fatal(events)
	}
	raw, _ := json.Marshal(events)
	if !strings.Contains(string(raw), `"type"`) || strings.Contains(string(raw), `"Type"`) {
		t.Fatal("login events do not use the public JSON field contract")
	}
	if strings.Contains(string(raw), "DO-NOT-EMIT") || strings.Contains(string(raw), "person@") {
		t.Fatal("private diagnostics escaped")
	}
	if err := loginLine("codex", string([]byte{255}), emit); err == nil {
		t.Fatal("invalid Unicode accepted")
	}
	stop := errors.New("consumer stopped")
	if err := loginLine("github", "https://github.com/login/device", func(LoginEvent) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("callback error ignored")
	}
}
func TestNativeAuthenticationEnvironmentAndArguments(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "DO-NOT-INHERIT")
	t.Setenv("GH_TOKEN", "DO-NOT-INHERIT")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "DO-NOT-INHERIT")
	for _, provider := range []string{"codex", "claude", "github"} {
		c := Connection{Provider: provider, NativeHome: join(t.TempDir(), "native"), HomeMode: "separate"}
		for _, value := range nativeEnvironment(c) {
			if strings.Contains(value, "DO-NOT-INHERIT") {
				t.Fatal("ambient token inherited")
			}
		}
		args, err := loginArgs(c)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "logout") || strings.Contains(joined, "--with-token") || strings.Contains(joined, "--with-api-key") || strings.Contains(joined, "--clipboard") {
			t.Fatal(args)
		}
	}
	if IdentityFingerprint("codex", "same") == IdentityFingerprint("claude", "same") || IdentityFingerprint("codex", "") != "" {
		t.Fatal("identity namespaces collide")
	}
}
func TestGitHubStatusDoesNotMistakeDamagedCredentialsForSignedOut(t *testing.T) {
	for _, test := range []struct{ raw, state string }{
		{`{"hosts":{}}`, "signed-out"},
		{`{"hosts":{"github.com":[]}}`, "signed-out"},
		{`{"hosts":{"github.com":[{"active":true,"state":"error","host":"github.com"}]}}`, "unknown"},
		{`{"hosts":{"github.com":[{"active":false,"state":"success","host":"github.com"}]}}`, "unknown"},
		{`{"hosts":{"github.com":[{"active":true,"state":"success","host":"github.com"}]}}`, "authenticated"},
		{`{"hosts":{"github.com":[{"active":true,"state":"success","host":"github.com"},{"active":true,"state":"success","host":"github.com"}]}}`, "unknown"},
	} {
		got, err := githubAuthenticationState([]byte(test.raw))
		if err != nil || got != test.state {
			t.Fatalf("status=%s wanted=%s err=%v", got, test.state, err)
		}
	}
	for _, raw := range []string{`{}`, `{"hosts":null}`, `PRIVATE-DIAGNOSTIC`, `{"hosts":{"github.com":[{"active":"true"}]}}`} {
		_, err := githubAuthenticationState([]byte(raw))
		requireCode(t, err, "connection_status")
		if strings.Contains(err.Error(), "PRIVATE-DIAGNOSTIC") {
			t.Fatal("raw diagnostic escaped")
		}
	}
}
func TestAuthenticationProcessFixture(t *testing.T) {
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "connection-json":
		fmt.Print(`{"state":"synthetic"}`)
		fmt.Fprint(os.Stderr, "PRIVATE-DIAGNOSTIC")
		os.Exit(0)
	case "connection-stall":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "connection-login":
		fmt.Println("PRIVATE-DIAGNOSTIC")
		fmt.Println("https://auth.openai.com/codex/device")
		fmt.Println("code: ABCD-1234")
		os.Exit(0)
	case "connection-huge":
		fmt.Print(strings.Repeat("x", (1<<20)+1))
		os.Exit(0)
	}
}
func TestOwnedAuthenticationTransportBoundsAndCancellation(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Darwin owned process launch remains unavailable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sha, err := executableHash(context.Background(), exe)
	if err != nil {
		t.Fatal(err)
	}
	c := Connection{Provider: "codex", Executable: exe, SHA256: sha, NativeHome: t.TempDir()}
	args := func(mode string) []string {
		return []string{"-test.run=^TestAuthenticationProcessFixture$", "--", mode}
	}
	result, err := run(context.Background(), c, args("connection-json"), 2*time.Second, nil)
	if err != nil || result.exit != 0 || string(result.stdout) != `{"state":"synthetic"}` {
		t.Fatal("owned fixture failed", err)
	}
	events := []LoginEvent{}
	_, err = run(context.Background(), c, args("connection-login"), 2*time.Second, func(line string) error {
		return loginLine("codex", line, func(e LoginEvent) error { events = append(events, e); return nil })
	})
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	_, err = run(context.Background(), c, args("connection-stall"), 100*time.Millisecond, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline lost", err)
	}
	var typed *core.Error
	if errors.As(err, &typed) && typed.Code == "connection_cleanup" {
		t.Fatal("settled cancellation became unknown", err)
	}
	_, err = run(context.Background(), c, args("connection-huge"), 2*time.Second, nil)
	if err == nil || strings.Contains(err.Error(), "PRIVATE-DIAGNOSTIC") {
		t.Fatal("output bound or sanitization failed", err)
	}
}
func TestNativeStatusOptIn(t *testing.T) {
	provider := os.Getenv("UCLI_CONNECTIONS_TEST_PROVIDER")
	if provider == "" {
		t.Skip("explicit native status opt-in not supplied")
	}
	s, err := New(join(t.TempDir(), "ucli"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Connect(context.Background(), ConnectOptions{Provider: provider, Label: "native-readonly-test", Executable: os.Getenv("UCLI_CONNECTIONS_TEST_EXECUTABLE"), NativeHome: os.Getenv("UCLI_CONNECTIONS_TEST_HOME")})
	if err != nil {
		t.Fatal(err)
	}
	// Only coarse state/version enter test receipts; identities and native paths
	// remain in the private temporary metadata owned by this test.
	t.Logf("provider=%s version=%s auth_state=%s identity_present=%t models=%d", provider, c.Version, c.AuthState, c.Identity != "", len(c.Models))
	before := c
	cached, err := s.Get(context.Background(), c.ID)
	if err != nil || !reflect.DeepEqual(before, cached) {
		t.Fatal("cached native metadata changed", err)
	}
}
