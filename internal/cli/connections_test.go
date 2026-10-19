package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

type fakeConnections struct {
	item                connections.Connection
	options             connections.ConnectOptions
	loginCalls, removed int
	login               func(context.Context, func(connections.LoginEvent) error) (connections.Connection, error)
	connect             func(context.Context) (connections.Connection, error)
}

func (f *fakeConnections) List(context.Context) ([]connections.Connection, error) {
	return []connections.Connection{f.item}, nil
}
func (f *fakeConnections) Get(context.Context, string) (connections.Connection, error) {
	return f.item, nil
}
func (f *fakeConnections) Connect(ctx context.Context, o connections.ConnectOptions) (connections.Connection, error) {
	f.options = o
	if f.connect != nil {
		return f.connect(ctx)
	}
	return f.item, nil
}
func (f *fakeConnections) Status(context.Context, string) (connections.Connection, error) {
	return f.item, nil
}
func (f *fakeConnections) Remove(context.Context, string) error { f.removed++; return nil }
func (f *fakeConnections) Login(ctx context.Context, _ string, emit func(connections.LoginEvent) error) (connections.Connection, error) {
	f.loginCalls++
	if f.login != nil {
		return f.login(ctx, emit)
	}
	return f.item, nil
}

func TestConnectionCommandVocabularyAndFlagScope(t *testing.T) {
	for _, args := range [][]string{{"login", "codex", "--separate", "--label", "work"}, {"accounts", "add", "claude", "--native-home", "profile"}, {"accounts", "status"}, {"accounts", "discover"}, {"logout", "connection"}, {"run", "--permission", "sandbox", "task"}} {
		if _, err := parse(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"tasks", "--separate"}, {"accounts", "status", "--executable", "native"}, {"login", "codex", "--permission", "sandbox"}, {"accounts", "add"}, {"logout"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("ignored invalid arguments: %v", args)
		}
	}
}

func TestSignInReusesExistingAuthAndNoInputNeverStartsLogin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, diagnostic bytes.Buffer
	o := newOutput(&out, &diagnostic, true, cancel)
	f := &fakeConnections{item: connections.Connection{ID: "connection", Provider: "codex", AuthState: "authenticated"}}
	err := applyConnectionCommand(ctx, f, invocation{command: "login", id: "codex", separate: true, connectionLabel: "work"}, o, nil)
	if err != nil || f.loginCalls != 0 || f.options.HomeMode != "separate" || f.options.Label != "work" {
		t.Fatalf("existing login restarted or intent lost: %+v %v", f, err)
	}
	f.item.AuthState = "signed-out"
	err = applyConnectionCommand(ctx, f, invocation{command: "login", id: "codex", noInput: true}, o, nil)
	if err == nil || problemFor(err).Exit != 3 || f.loginCalls != 0 {
		t.Fatalf("no-input started interactive login: %v", err)
	}
	f.item.AuthState = "unknown"
	err = applyConnectionCommand(ctx, f, invocation{command: "login", id: "agy", noInput: true}, o, nil)
	if err == nil || problemFor(err).Code != "sign_in_unknown" || f.loginCalls != 0 {
		t.Fatalf("unknown native state was presented as signed out: %v", err)
	}
	if err = o.finish(); err != nil {
		t.Fatal(err)
	}
	jsonLines(t, out.String())
}

func TestUnlinkDoesNotCallNativeLoginOrStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	o := newOutput(&out, &out, true, cancel)
	f := &fakeConnections{}
	if err := applyConnectionCommand(ctx, f, invocation{command: "logout", id: "connection"}, o, nil); err != nil {
		t.Fatal(err)
	}
	if f.removed != 1 || f.loginCalls != 0 {
		t.Fatal("unlink invoked native authentication")
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"native_credentials_revoked":false`) {
		t.Fatal("native revocation boundary missing")
	}
}

func TestNativeLoginCancellationPreservesCleanupFailureWithoutRawDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	o := newOutput(&out, &out, true, func() {})
	f := &fakeConnections{login: func(ctx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
		cancel()
		<-ctx.Done()
		return connections.Connection{}, errors.Join(ctx.Err(), errors.New("private native diagnostic"))
	}}
	_, err := performLoginOperation(ctx, func(ctx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
		return f.Login(ctx, "connection", emit)
	}, o, nil)
	if err == nil || problemFor(err).Exit != 9 || strings.Contains(err.Error(), "private native diagnostic") {
		t.Fatalf("cleanup uncertainty or redaction lost: %v", err)
	}
	if err = o.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSignInFrameKeepsDeviceInstructionsAndRecovery(t *testing.T) {
	event := connections.LoginEvent{URL: "https://github.com/login/device", Code: "ABCD-EFGH", Message: "Complete sign-in in your browser"}
	for _, width := range []int{40, 80} {
		frame := nativeLoginFrame(event, width, 24)
		text := terminalPickerText(frame)
		if !strings.Contains(text, event.URL) || !strings.Contains(text, event.Code) || !strings.Contains(text, "Esc cancel") {
			t.Fatalf("sign-in instructions missing: %s", text)
		}
	}
	tiny := terminalPickerText(nativeLoginFrame(event, 24, 3))
	if !strings.Contains(tiny, "Resize") || !strings.Contains(tiny, "Esc cancel") {
		t.Fatalf("tiny viewport lost recovery: %s", tiny)
	}
	state := mergeLoginEvent(connections.LoginEvent{}, connections.LoginEvent{URL: event.URL})
	state = mergeLoginEvent(state, connections.LoginEvent{Code: event.Code, Message: event.Message})
	if state.URL != event.URL || state.Code != event.Code {
		t.Fatal("separate device events discarded earlier sign-in instructions")
	}
}

func TestBrokenSignInOutputCancelsAndJoinsNativeLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	o := newOutput(brokenWriter{}, brokenWriter{}, true, cancel)
	settled := make(chan struct{})
	f := &fakeConnections{login: func(ctx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
		defer close(settled)
		if err := emit(connections.LoginEvent{Message: "Opening native sign-in"}); err != nil {
			return connections.Connection{}, err
		}
		<-ctx.Done()
		return connections.Connection{}, ctx.Err()
	}}
	if _, err := performLoginOperation(ctx, func(ctx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
		return f.Login(ctx, "connection", emit)
	}, o, nil); err == nil {
		t.Fatal("broken output did not cancel sign-in")
	}
	select {
	case <-settled:
	default:
		t.Fatal("native login remained active after output failure")
	}
	_ = o.finish()
}

func TestSignInDeviceEventsAndEscapeReturnToDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var out bytes.Buffer
	o := newOutput(&out, &out, false, cancel)
	terminal := newTaskPickerTestTerminal()
	settled := make(chan struct{})
	f := &fakeConnections{login: func(ctx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
		defer close(settled)
		if err := emit(connections.LoginEvent{URL: "https://github.com/login/device"}); err != nil {
			return connections.Connection{}, err
		}
		if err := emit(connections.LoginEvent{Code: "ABCD-EFGH"}); err != nil {
			return connections.Connection{}, err
		}
		<-ctx.Done()
		return connections.Connection{}, ctx.Err()
	}}
	result := make(chan error, 1)
	go func() {
		_, err := performLoginOperation(ctx, func(ctx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
			return f.Login(ctx, "connection", emit)
		}, o, terminal)
		result <- err
	}()
	frame := awaitTaskPickerFrame(t, terminal, "ABCD-EFGH", true)
	if !strings.Contains(terminalPickerText(frame), "https://github.com/login/device") {
		t.Fatal("device code replaced the URL")
	}
	terminal.events <- keyEvent{kind: keyEscape}
	select {
	case err := <-result:
		if err != errLoginDismissed {
			t.Fatalf("escape changed whole-session cancellation: %v", err)
		}
		s := &session{out: o, task: &core.Task{Draft: "saved draft"}, selection: core.Defaults()}
		if err = s.loginResult(err); err != nil || s.task.Draft != "saved draft" || s.selection != core.Defaults() || ctx.Err() != nil {
			t.Fatal("escape failed to preserve composition/session")
		}
	case <-ctx.Done():
		t.Fatal("escape did not settle native login")
	}
	select {
	case <-settled:
	default:
		t.Fatal("escaped login remained active")
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestSignInPreflightCanBeCanceledBeforeLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var out bytes.Buffer
	o := newOutput(&out, &out, false, cancel)
	terminal := newTaskPickerTestTerminal()
	started, settled := make(chan struct{}), make(chan struct{})
	f := &fakeConnections{connect: func(ctx context.Context) (connections.Connection, error) {
		close(started)
		defer close(settled)
		<-ctx.Done()
		return connections.Connection{}, ctx.Err()
	}}
	result := make(chan error, 1)
	go func() {
		_, err := performLoginOperation(ctx, func(ctx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
			return linkNativeConnection(ctx, f, invocation{command: "login", id: "codex"}, emit)
		}, o, terminal)
		result <- err
	}()
	awaitTaskPickerFrame(t, terminal, "Checking native sign-in", true)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("native status preflight never started")
	}
	terminal.events <- keyEvent{kind: keyEscape}
	select {
	case err := <-result:
		if err != errLoginDismissed || ctx.Err() != nil || f.loginCalls != 0 {
			t.Fatalf("preflight cancellation changed session or started login: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("native preflight did not settle after Escape")
	}
	select {
	case <-settled:
	default:
		t.Fatal("native preflight remained active")
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
}
