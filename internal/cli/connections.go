package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

type connectionService interface {
	List(context.Context) ([]connections.Connection, error)
	Get(context.Context, string) (connections.Connection, error)
	Connect(context.Context, connections.ConnectOptions) (connections.Connection, error)
	Status(context.Context, string) (connections.Connection, error)
	Login(context.Context, string, func(connections.LoginEvent) error) (connections.Connection, error)
	Remove(context.Context, string) error
}

var errLoginDismissed = errors.New("native login dismissed")

func isConnectionCommand(command string) bool {
	return command == "login" || command == "logout" || strings.HasPrefix(command, "accounts ")
}

// Sign-in metadata has its own store. No task DB, microphone or model run is
// needed to connect, inspect or unlink an account.
func connectionCommand(ctx context.Context, p invocation, in io.Reader, o *output) int {
	home, err := invocationHome(p)
	if err != nil {
		return o.problem(err)
	}
	svc, err := connections.New(home)
	if err != nil {
		return o.problem(connectionProblem(err))
	}
	if p.command == "accounts discover" {
		found, err := svc.Discover(ctx)
		if err != nil {
			return o.problem(connectionProblem(err))
		}
		if o.json {
			_ = o.data("native_providers", found)
		} else {
			for _, item := range found {
				_ = o.text(fmt.Sprintf("%s | available %t | %s\n", item.Provider, item.Available, item.Reason))
			}
			_ = o.text("Connect: ucli login PROVIDER | Separate account: --separate --label NAME\n")
		}
		return 0
	}
	if p.command == "login" && !p.noInput && !p.json && isTerminal(in) && isTerminal(o.out) && !p.set["plain"] && os.Getenv("TERM") != "dumb" {
		o.terminal = newTerminalUI(in, o.out, o.cancel, !p.set["no-color"] && os.Getenv("NO_COLOR") == "", terminalOptionsFor(p))
		location, _ := os.Getwd()
		if err := o.terminal.setChrome(terminalChrome{Version: Version, Location: location, Model: "Native sign-in", Context: "No task is submitted", Panel: true}); err != nil {
			return o.problem(err)
		}
		if err := o.terminal.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "Sign-in · Esc back · Ctrl+C cancel", Dim: true}}, CursorRow: -1}); err != nil {
			return o.problem(err)
		}
		o.out, o.errOut = o.terminal.writer, o.terminal.writer
	}
	if p.command == "login" && p.id == "" {
		if p.noInput || p.json || !isTerminal(in) || (o.terminal == nil && !isTerminal(o.out)) {
			return o.problem(usageError("choose ucli login codex, claude, github or agy"))
		}
		s := &session{out: o, terminal: o.terminal}
		if s.terminal == nil {
			s.input = newLineInput(terminalInput(in))
			defer close(s.input.stop)
		}
		p.id, err = s.chooseLoginProvider(ctx)
		if err != nil {
			return o.problem(connectionProblem(err))
		}
		if p.id == "" {
			return 0
		}
		action, err := s.chooseConnectionAction(ctx, p.id)
		if err != nil {
			return o.problem(connectionProblem(err))
		}
		if action == "" {
			return 0
		}
		if action == "check" {
			p.command = "accounts add"
		}
	}
	if err := applyConnectionCommand(ctx, svc, p, o, o.terminal); err != nil {
		if err == io.EOF {
			return 0
		}
		return o.problem(connectionProblem(err))
	}
	return 0
}

func connectionProblem(err error) error {
	if err == errLoginDismissed {
		return core.Problem("login_canceled", "Sign-in canceled. The connection is retained; accounts status checks its current state.", 130)
	}
	var problem *core.Error
	if errors.As(err, &problem) {
		return err
	}
	if onlyContextError(err) {
		return optionalProblem(err)
	}
	return core.Problem("connection_failed", "Connection operation failed or cleanup was unconfirmed. Native sign-in was not revoked; inspect accounts status before retrying.", 9)
}

func nativeProvider(value string) string {
	switch value {
	case "codex", "claude", "github", "agy":
		return value
	case "gh":
		return "github"
	case "antigravity":
		return "agy"
	}
	return ""
}

func applyConnectionCommand(ctx context.Context, svc connectionService, p invocation, o *output, terminal *terminalUI) error {
	switch p.command {
	case "accounts list":
		items, err := svc.List(ctx)
		if err != nil {
			return err
		}
		if o.json {
			return o.data("connections", items)
		}
		if len(items) == 0 {
			return o.text("No linked native accounts. Use ucli login to connect one.\n")
		}
		for _, item := range items {
			if err := showConnection(item, o); err != nil {
				return err
			}
		}
		return nil
	case "accounts remove", "logout":
		if err := svc.Remove(ctx, p.id); err != nil {
			return err
		}
		if o.json {
			return o.data("connection_removed", map[string]any{"id": p.id, "native_credentials_revoked": false})
		}
		return o.text("Connection unlinked from UCLI. Native sign-in and saved tasks are retained.\n")
	case "accounts status":
		ids := []string{p.id}
		if p.id == "" {
			items, err := svc.List(ctx)
			if err != nil {
				return err
			}
			ids = nil
			for _, item := range items {
				ids = append(ids, item.ID)
			}
		}
		for _, id := range ids {
			item, err := svc.Status(ctx, id)
			if err != nil {
				return err
			}
			if err := showConnection(item, o); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			if o.json {
				return o.data("connections", []connections.Connection{})
			}
			return o.text("No linked accounts. Use ucli login PROVIDER.\n")
		}
		return nil
	case "login", "accounts add":
		var screen taskPickerTerminal
		if terminal != nil {
			screen = terminal
		}
		item, err := performLoginOperation(ctx, func(jobCtx context.Context, emit func(connections.LoginEvent) error) (connections.Connection, error) {
			return linkNativeConnection(jobCtx, svc, p, emit)
		}, o, screen)
		if err != nil {
			return err
		}
		return showConnection(item, o)
	}
	return usageError("unknown connection command")
}

func linkNativeConnection(ctx context.Context, svc connectionService, p invocation, emit func(connections.LoginEvent) error) (connections.Connection, error) {
	var item connections.Connection
	var err error
	if err = emit(connections.LoginEvent{Message: "Checking native sign-in; no AI task is submitted"}); err != nil {
		return item, err
	}
	if provider := nativeProvider(p.id); provider != "" {
		mode := "existing"
		if p.separate {
			mode = "separate"
		}
		nativeHome, executable := p.nativeHome, p.executable
		if nativeHome != "" {
			nativeHome, err = filepath.Abs(nativeHome)
			if err != nil {
				return item, usageError("native account directory could not be resolved")
			}
		}
		if executable != "" {
			executable, err = filepath.Abs(executable)
			if err != nil {
				return item, usageError("native executable could not be resolved")
			}
		}
		item, err = svc.Connect(ctx, connections.ConnectOptions{Provider: provider, Label: p.connectionLabel, Executable: executable, NativeHome: nativeHome, HomeMode: mode})
	} else {
		if p.command == "accounts add" || p.connectionLabel != "" || p.nativeHome != "" || p.executable != "" || p.separate {
			return item, usageError("choose provider codex, claude, github or agy when adding or configuring a connection")
		}
		item, err = svc.Get(ctx, p.id)
		if err == nil {
			item, err = svc.Status(ctx, item.ID)
		}
	}
	if err != nil {
		return item, err
	}
	if p.command == "accounts add" || item.AuthState == "authenticated" {
		return item, nil
	}
	if p.noInput {
		if item.AuthState != "signed-out" {
			return item, core.Problem("sign_in_unknown", "Native sign-in could not be confirmed. Check accounts status; use the native client if its login interface is unsupported.", 3)
		}
		return item, core.Problem("sign_in_required", "Connection saved. Run ucli login "+item.ID+" to finish native sign-in.", 3)
	}
	return svc.Login(ctx, item.ID, emit)
}

func showConnection(item connections.Connection, o *output) error {
	if o.json {
		return o.data("connection", item)
	}
	next := "Open /models to choose a supported model. Identity and runtime compatibility are checked again before sending."
	switch {
	case item.Provider == "agy":
		next = fmt.Sprintf("%d native model names cached. AGY execution and sign-in verification are not supported in UCLI; use Antigravity itself to run them.", len(item.Models))
	case item.Provider == "github":
		next = "This connects your local GitHub CLI identity only. It supplies no AI models and does not create a hosted UCLI account."
	case item.AuthState == "signed-out":
		next = "Your native account is signed out. Choose Sign in in /login to complete the provider's browser/device steps."
	case item.AuthState == "identity-changed":
		next = "The native account changed. This connection is blocked; explicitly unlink and reconnect the intended account."
	case item.AuthState != "authenticated":
		next = "Sign-in was not confirmed. Check the native client, then refresh here; another login may not be needed."
	}
	return o.text(fmt.Sprintf("%s | %s | %s\n%s\nConnection reference: %s (native credentials stay with the provider).\n", item.Label, providerName(item.Provider), authenticationLabel(item.AuthState), next, item.ID))
}

func (s *session) chooseLoginProvider(ctx context.Context) (string, error) {
	return s.choose(ctx, "Connect an account — your draft is kept", []choice{{"codex", "Codex - use your ChatGPT sign-in in the Codex CLI"}, {"claude", "Claude Code - use your Claude sign-in; reviewed CLI version required"}, {"github", "GitHub - local GitHub CLI identity, no AI models"}, {"agy", "Antigravity (AGY) - inspect model names; UCLI execution unavailable"}})
}

func providerName(id string) string {
	switch id {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "agy":
		return "Antigravity (AGY)"
	case "github":
		return "GitHub"
	default:
		return "Native account"
	}
}

func (s *session) chooseConnectionAction(ctx context.Context, identifier string) (string, error) {
	name := providerName(nativeProvider(identifier))
	rows := []choice{{"check", "Use the account already signed in on this computer - check and link only"}}
	if nativeProvider(identifier) == "agy" {
		rows = []choice{{"check", "Inspect Antigravity model names - no UCLI task execution or sign-in"}}
	} else {
		rows = append(rows, choice{"login", "Sign in through " + name + " - browser/device steps if needed"})
	}
	return s.choose(ctx, name+" - choose what to do; your draft is kept", rows)
}

func (s *session) connectionAction(ctx context.Context, verb, identifier string) error {
	if verb == "/logout" && identifier == "" {
		return usageError("use /logout CONNECTION_ID to unlink one UCLI connection")
	}
	if identifier == "" {
		var err error
		identifier, err = s.chooseLoginProvider(ctx)
		if err != nil || identifier == "" {
			return err
		}
	}
	svc, err := connections.New(s.app.Home)
	if err != nil {
		return connectionProblem(err)
	}
	p := invocation{command: strings.TrimPrefix(verb, "/"), id: identifier, home: s.app.Home}
	if verb == "/login" {
		action, err := s.chooseConnectionAction(ctx, identifier)
		if err != nil || action == "" {
			return err
		}
		if action == "check" {
			if nativeProvider(identifier) != "" {
				p.command = "accounts add"
			} else {
				var screen taskPickerTerminal
				if s.terminal != nil {
					screen = s.terminal
				}
				item, err := performLoginOperation(ctx, func(ctx context.Context, _ func(connections.LoginEvent) error) (connections.Connection, error) {
					return svc.Status(ctx, identifier)
				}, s.out, screen)
				if err == nil {
					err = showConnection(item, s.out)
				}
				return s.loginResult(err)
			}
		}
	}
	return s.loginResult(applyConnectionCommand(ctx, svc, p, s.out, s.terminal))
}

func (s *session) loginResult(err error) error {
	s.accountLabels = nil // Refresh only after an explicit connection action.
	if err == errLoginDismissed {
		return s.out.text("Sign-in canceled. Your draft and choices are kept.\n")
	}
	return err
}

type loginOutcome struct {
	connection connections.Connection
	err        error
}

func performLoginOperation(ctx context.Context, operation func(context.Context, func(connections.LoginEvent) error) (connections.Connection, error), o *output, terminal taskPickerTerminal) (connections.Connection, error) {
	_ = o.notice("Checking native sign-in | no AI task submitted", toneInfo)
	_ = o.activity("looking", false)
	defer o.activity("", true)
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan connections.LoginEvent, 16)
	wake := make(chan struct{}, 1)
	finished := make(chan loginOutcome, 1)
	go func() {
		item, err := operation(jobCtx, func(event connections.LoginEvent) error {
			select {
			case events <- event:
				select {
				case wake <- struct{}{}:
				default:
				}
				return nil
			case <-jobCtx.Done():
				return jobCtx.Err()
			default:
				return core.Problem("login_output_limit", "Native sign-in produced too many updates; no AI task was submitted.", 9)
			}
		})
		finished <- loginOutcome{item, err}
		select {
		case wake <- struct{}{}:
		default:
		}
	}()
	join := func(reason error) (connections.Connection, error) {
		cancel()
		select {
		case result := <-finished:
			if result.err != nil && !onlyContextError(result.err) {
				return connections.Connection{}, connectionProblem(result.err)
			}
			return connections.Connection{}, reason
		case <-time.After(8 * time.Second):
			return connections.Connection{}, core.Problem("login_cleanup_unconfirmed", "Native sign-in cleanup was not confirmed. Check accounts status before another login.", 9)
		}
	}
	state := connections.LoginEvent{Message: "Waiting for native sign-in"}
	for {
		for {
			select {
			case result := <-finished:
				if ctx.Err() != nil {
					return joinResult(result, ctx.Err())
				}
				return result.connection, result.err
			case event := <-events:
				state = mergeLoginEvent(state, event)
				if terminal != nil {
					_ = o.notice(state.Message, toneInfo)
				}
				if terminal == nil {
					var writeErr error
					if o.json {
						writeErr = o.data("login_action", event)
					} else {
						writeErr = o.progress(strings.TrimSpace(event.Message+"\n"+event.URL+"\n"+event.Code) + "\n")
					}
					if writeErr != nil {
						return join(writeErr)
					}
				}
			default:
				goto updated
			}
		}
	updated:
		if terminal == nil {
			select {
			case <-wake:
				continue
			case <-ctx.Done():
				return join(ctx.Err())
			}
		}
		width, height := 80, 24
		if sized, ok := terminal.(interface{ size() (int, int) }); ok {
			width, height = sized.size()
		}
		if err := terminal.paint(nativeLoginFrame(state, width, height)); err != nil {
			return join(err)
		}
		event, err := terminal.nextOr(ctx, wake)
		if err != nil {
			return join(err)
		}
		if event.kind == keyEscape {
			return join(errLoginDismissed)
		}
		if event.kind == keyEOF {
			return join(io.EOF)
		}
	}
}

func mergeLoginEvent(state, event connections.LoginEvent) connections.LoginEvent {
	if event.Type != "" {
		state.Type = event.Type
	}
	if event.Message != "" {
		state.Message = event.Message
	}
	if event.URL != "" {
		state.URL = event.URL
	}
	if event.Code != "" {
		state.Code = event.Code
	}
	return state
}

func nativeLoginFrame(state connections.LoginEvent, width, height int) terminalFrame {
	lines := []terminalLine{{Text: "Connect native account", Dim: true}}
	for _, value := range []string{state.URL, state.Code, state.Message} {
		if value == "" {
			continue
		}
		for _, text := range workflowWrap(value, max(1, width-1)) {
			lines = append(lines, terminalLine{Text: text})
		}
	}
	hint := "Esc cancel · draft kept"
	if cellWidth(hint) >= width {
		hint = "Esc cancel"
	}
	if len(lines)+1 > height || width < 24 {
		lines = []terminalLine{{Text: "Resize for sign-in link"}}
	}
	lines = append(lines, terminalLine{Text: hint, Dim: true})
	return terminalFrame{Lines: lines, CursorRow: -1}
}

func joinResult(result loginOutcome, reason error) (connections.Connection, error) {
	if result.err != nil && !onlyContextError(result.err) {
		return connections.Connection{}, connectionProblem(result.err)
	}
	return connections.Connection{}, reason
}
