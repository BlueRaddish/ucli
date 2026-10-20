package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

type line struct {
	text     string
	complete bool
	literal  bool // bracketed paste never invokes a slash action
	err      error
}
type lineInput struct {
	request chan chan line
	stop    chan struct{}
	read    func(context.Context) line
}

func newLineInput(reader io.Reader) *lineInput {
	i := &lineInput{request: make(chan chan line), stop: make(chan struct{})}
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if n := strings.IndexByte(string(data), '\n'); n >= 0 {
				return n + 1, data[:n+1], nil
			}
			if atEOF && len(data) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		for {
			select {
			case <-i.stop:
				return
			case reply := <-i.request:
				result := line{err: io.EOF}
				if scanner.Scan() {
					text := scanner.Text()
					result = line{text: strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r"), complete: strings.HasSuffix(text, "\n")}
					if !result.complete {
						result.err = io.EOF
					}
				} else if scanner.Err() != nil {
					result.err = scanner.Err()
				}
				select {
				case reply <- result:
				case <-i.stop:
					return
				}
			}
		}
	}()
	return i
}

func (i *lineInput) next(ctx context.Context) line {
	if i.read != nil {
		return i.read(ctx)
	}
	reply := make(chan line, 1)
	select {
	case i.request <- reply:
	case <-ctx.Done():
		return line{err: ctx.Err()}
	}
	select {
	case result := <-reply:
		return result
	case <-ctx.Done():
		return line{err: ctx.Err()}
	}
}

type session struct {
	app           *app.App
	selection     core.Selection
	task          *core.Task
	cwd           string
	demo          bool
	recoveryID    string
	historyOpen   bool
	historyNext   int
	taskSearch    string
	taskCursor    string
	taskFocus     string
	input         *lineInput
	terminal      *terminalUI
	out           *output
	accountLabels map[string]string // Cached metadata; typing never polls a native client.
	chrome        terminalChrome
	chromeSet     bool
}

func interact(ctx context.Context, a *app.App, p invocation, in io.Reader, o *output) int {
	s := &session{app: a, selection: p.selection.WithDefaults(), cwd: p.cwd, demo: p.demo, out: o}
	if isTerminal(in) && isTerminal(o.out) && !p.set["plain"] && os.Getenv("TERM") != "dumb" {
		s.terminal = newTerminalUI(in, o.out, o.cancel, !p.set["no-color"] && os.Getenv("NO_COLOR") == "", terminalOptionsFor(p))
		o.terminal = s.terminal
		o.out, o.errOut = s.terminal.writer, s.terminal.writer
		s.input = &lineInput{read: s.readTerminalLine}
	} else {
		s.input = newLineInput(terminalInput(in))
		defer close(s.input.stop)
	}
	if s.cwd == "" {
		var err error
		s.cwd, err = os.Getwd()
		if err != nil {
			return o.problem(err)
		}
	}
	var err error
	s.cwd, err = filepath.Abs(s.cwd)
	if err != nil {
		return o.problem(err)
	}
	if err = s.syncTerminalChrome(); err != nil {
		return o.problem(err)
	}
	if s.terminal == nil {
		_ = o.text("UCLI - choose an agent and keep your work\nEnter adds a line to a saved draft; /send submits it. /help lists actions.\n")
		_ = o.text("Static line interface. There is no live steering or detach.\n")
	} else {
		_ = o.text("Describe your task below. Enter saves your draft; /send submits it.\nTab opens models, accounts, effort and other actions.\n")
	}
	if s.demo {
		_ = o.text("DEMO enabled: agent responses use simulation or explicit native echo. Voice runs only when you choose it.\n")
	} else {
		_ = o.text("Use /login to connect a native account. /demo enables local simulation or explicit native echo.\n")
	}
	restored, err := a.RecentDraft(ctx, s.cwd)
	if err != nil {
		return o.problem(err)
	}
	s.task = restored
	if s.task != nil {
		s.selection = s.task.Selection.WithDefaults()
		if p.selection.Model != "" {
			s.selection.Model = p.selection.Model
		}
		if p.selection.Account != "" {
			s.selection.Account = p.selection.Account
		}
		if p.selection.Effort != "" {
			s.selection.Effort = p.selection.Effort
		}
		if p.selection.Permission != "" {
			s.selection.Permission = p.selection.Permission
		}
		_ = o.text("Restored a saved draft in this directory. Nothing has been submitted.\n")
		if s.terminal != nil && s.task.Draft != "" {
			_ = o.text("Restored draft (latest text):\n" + streamFragment(s.task.Draft) + "\n")
			_ = o.notice("Draft restored | /send submits it", toneInfo)
		}
	}
	if s.task == nil || s.task.Draft == "" {
		_ = o.notice("Ready | Enter saves a draft; /send submits it", toneInfo)
	}
	_ = o.activity("wave", false)
	for {
		s.status()
		answer := s.input.next(ctx)
		if answer.err != nil {
			// Preserve processed partial input even after cancellation, without
			// reusing an already canceled execution context or executing commands.
			if answer.text != "" {
				saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				err := s.appendDraft(saveCtx, answer.text)
				cancel()
				if err != nil {
					return o.problem(err)
				}
			}
			if errors.Is(answer.err, io.EOF) {
				_ = o.text("Input closed. Saved drafts remain; no work was submitted by EOF.\n")
				return 0
			}
			return o.problem(answer.err)
		}
		if strings.TrimSpace(answer.text) == "" {
			continue
		}
		if answer.literal || !strings.HasPrefix(answer.text, "/") {
			if err := s.appendDraft(ctx, answer.text); err != nil {
				code := o.problem(err)
				if s.recoveryID == "" {
					return code
				}
			}
			continue
		}
		quit, err := s.action(ctx, answer.text)
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = o.text("Input closed. Saved drafts remain unchanged.\n")
				return 0
			}
			code := o.problem(err)
			if code == 9 || code == 124 || code == 130 || ctx.Err() != nil {
				return code
			}
		}
		if quit {
			return 0
		}
	}
}

func (s *session) status() {
	if s.terminal != nil {
		return
	}
	if s.task == nil {
		_ = s.out.text("\nNew task | fresh context\n")
	} else {
		_ = s.out.text(fmt.Sprintf("\nTask %s | %s | revision %d\n", s.task.ID, s.task.Status, s.task.Revision))
		if s.task.Draft != "" {
			label := "Saved draft:\n"
			if s.recoveryID != "" {
				label = "Unsent input (saved separately):\n"
			}
			_ = s.out.text(label + s.task.Draft + "\n")
		}
	}
	if s.recoveryID != "" {
		_ = s.out.text("Draft conflict. Open recovery task " + s.recoveryID + " through /tasks, or choose /new.\nEditing and Send are blocked until you choose a task explicitly.\n")
	}
	_ = s.out.text(fmt.Sprintf("Next: model %s | effort %s | account %s | permission %s\n", s.selection.Model, s.selection.Effort, s.selection.Account, s.selection.Permission))
	if s.historyNext != 0 {
		_ = s.out.text("History: /older for earlier attempts | /result for recent results\n")
	}
	if s.recoveryID != "" {
		_ = s.out.text("Choices: /tasks /new /result /help /exit\nSend blocked until you choose a task explicitly\n> ")
	} else {
		_ = s.out.text("Choices: /models /accounts /effort /tasks /voice /workflow /new /result /help /exit\nSend: /send | Enter adds text to the draft\n> ")
	}
}

func (s *session) appendDraft(ctx context.Context, text string) error {
	if s.recoveryID != "" {
		_ = s.out.text("This additional line was not saved; copy it before choosing a task:\n" + text + "\n")
		return core.Problem("draft_conflict", "Choose /tasks or /new before editing or sending; the recovery draft remains unsent.", 5)
	}
	if s.task != nil && s.task.Draft != "" {
		text = s.task.Draft + "\n" + text
	}
	return s.saveDraft(ctx, text)
}

func (s *session) saveDraft(ctx context.Context, text string) error {
	id := ""
	revision := 0
	previous := ""
	if s.task != nil {
		id = s.task.ID
		revision = s.task.Revision
		previous = s.task.Draft
	}
	task, err := s.app.DraftAt(ctx, id, text, s.cwd, s.selection, &revision)
	if err != nil {
		if s.markConflict(err, text) {
			return err
		}
		// Keep the input visible if durable storage itself failed. Do not claim saved.
		_ = s.out.text("Draft could not be saved. Copy this text before leaving:\n" + text + "\n")
		return err
	}
	s.task = &task
	if s.terminal != nil {
		if text != previous {
			added := text
			if previous != "" && strings.HasPrefix(text, previous+"\n") {
				added = strings.TrimPrefix(text, previous+"\n")
			}
			_ = s.out.text("Saved draft:\n" + added + "\n")
		}
		_ = s.out.notice("Draft saved | /send submits it", toneSuccess)
	}
	return nil
}

func (s *session) markConflict(err error, text string) bool {
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != "revision_conflict" || problem.TaskID == "" {
		return false
	}
	s.recoveryID = problem.TaskID
	if s.task != nil {
		s.task.Draft = text
	}
	_ = s.out.text("The task changed elsewhere. Your input is an unsent recovery draft.\nUse /tasks to review it, or /new for fresh work; nothing was resubmitted.\n")
	return true
}

func (s *session) action(ctx context.Context, text string) (bool, error) {
	verb, argument, hasArgument := strings.Cut(text, " ")
	// Completion inserts a trailing delimiter for arguments. Whitespace alone
	// is not an argument; preserve the actual bytes of explicit /draft TEXT.
	if hasArgument && (argument == "" || verb != "/draft" && strings.TrimSpace(argument) == "") {
		argument, hasArgument = "", false
	}
	if hasArgument && verb != "/draft" && verb != "/workflow" && verb != "/voice" && verb != "/login" && verb != "/logout" {
		return false, usageError("" + verb + " takes no text; use /draft TEXT for literal input")
	}
	if s.recoveryID != "" {
		switch verb {
		case "/send", "/models", "/accounts", "/effort", "/permission", "/recover":
			return false, core.Problem("draft_conflict", "Choose /tasks or /new before changing or sending this task; the recovery draft remains unsent.", 5)
		case "/draft":
			if hasArgument {
				_ = s.out.text("Replacement text was not saved; copy it before choosing a task:\n" + argument + "\n")
				return false, core.Problem("draft_conflict", "Choose /tasks or /new before replacing the draft.", 5)
			}
		}
	}
	switch verb {
	case "/exit":
		return true, nil
	case "/help":
		_ = s.out.text(interactiveHelp())
	case "/new":
		s.task = nil
		s.recoveryID = ""
		s.historyOpen, s.historyNext = false, 0
		s.selection = core.Defaults()
		_ = s.out.text("New task selected. Earlier tasks and drafts remain in /tasks.\n")
		_ = s.out.notice("New task | enter text to create a draft", toneInfo)
	case "/demo":
		s.demo = true
		_ = s.out.text("DEMO enabled: agent responses use simulation or explicit native echo. Voice runs only when you choose it.\n")
	case "/draft":
		if hasArgument {
			return false, s.saveDraft(ctx, argument)
		}
		if s.task == nil || s.task.Draft == "" {
			_ = s.out.text("No saved draft. Type your task, then /send.\n")
		} else {
			label := "Saved draft:\n"
			if s.recoveryID != "" {
				label = "Unsent input saved in recovery task " + s.recoveryID + ":\n"
			}
			_ = s.out.text(label + s.task.Draft + "\n")
		}
	case "/models", "/accounts", "/effort", "/permission":
		return false, s.selectSetting(ctx, verb)
	case "/login", "/logout":
		return false, s.connectionAction(ctx, verb, strings.TrimSpace(argument))
	case "/tasks":
		return false, s.selectTask(ctx)
	case "/voice":
		text, err := s.voiceText(ctx, argument)
		if err != nil {
			return false, err
		}
		if text != "" {
			if err := s.appendDraft(ctx, text); err != nil {
				return false, err
			}
			_ = s.out.text("Voice text saved for review. Nothing was sent; /send submits the saved draft.\n")
		}
	case "/workflow":
		path := argument
		if !hasArgument {
			var err error
			path, err = s.promptWorkflowFile(ctx)
			if err != nil || path == "" {
				return false, err
			}
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.cwd, path)
		}
		return false, inspectWorkflow(ctx, invocation{command: "workflow view", file: path}, nil, s.out, s.terminal)
	case "/result":
		return false, s.showResult(ctx, 0)
	case "/older":
		if !s.historyOpen {
			_ = s.out.text("Use /result to open recent results first.\n")
			break
		}
		if s.historyNext == 0 {
			_ = s.out.text("No older attempts. /result returns to recent results.\n")
			break
		}
		return false, s.showResult(ctx, s.historyNext)
	case "/explain":
		route, err := s.app.Explain(ctx, s.options())
		if err != nil {
			return false, err
		}
		showRoute(route, s.out)
	case "/recover":
		if s.task == nil {
			return false, usageError("choose a task with /tasks before /recover")
		}
		attempt, err := s.app.Recover(ctx, s.task.ID)
		if err != nil {
			return false, err
		}
		_ = s.out.text("Reconciled demo outcome: " + attempt.State + ". No work was replayed.\n")
		s.historyOpen, s.historyNext = false, 0
		return false, s.refresh(ctx)
	case "/send":
		if s.task == nil || strings.TrimSpace(s.task.Draft) == "" {
			return false, usageError("type the work to do before /send")
		}
		submitted := s.task.Draft
		s.historyOpen, s.historyNext = false, 0
		_, runErr := runTask(ctx, s.app, s.options(), s.out)
		if runErr != nil && s.markConflict(runErr, submitted) {
			return false, runErr
		}
		if err := s.refresh(ctx); err != nil {
			return false, err
		}
		s.selection = s.task.Selection.WithDefaults()
		if runErr != nil {
			return false, runErr
		}
		_ = s.out.text("Follow up in this task, inspect /result, or choose /new for fresh context.\n")
	case "/detach", "/native", "/steer", "/cancel", "/retry", "/resume":
		return false, usageError(verb + " is unavailable in this foreground milestone; /result inspects work, /recover reconciles a stopped demo, Ctrl+C interrupts the owned run")
	default:
		return false, usageError("unknown action " + verb + "; use /help, or /draft TEXT for text beginning with a slash")
	}
	return false, nil
}

func (s *session) options() app.Options {
	o := app.Options{CWD: s.cwd, Selection: s.selection, Demo: s.demo}
	if s.task != nil {
		o.TaskID, o.Input = s.task.ID, s.task.Draft
		revision := s.task.Revision
		o.ExpectedRevision = &revision
	}
	return o
}

func (s *session) refresh(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	task, _, err := s.app.TaskPage(ctx, s.task.ID, 0, 1)
	if err != nil {
		return err
	}
	s.task = &task
	return nil
}

func (s *session) showResult(ctx context.Context, beforeRevision int) error {
	if s.task == nil {
		_ = s.out.text("No task selected. Choose /tasks or enter a new task.\n")
		return nil
	}
	task, attempts, paging, err := loadHistory(ctx, s.app, s.task.ID, beforeRevision, 5)
	if err != nil {
		return err
	}
	// Browsing saved history must not replace the displayed draft/CAS revision.
	showTask(task, attempts, paging, s.out)
	s.historyOpen, s.historyNext = true, paging.NextBeforeRevision
	if s.historyNext != 0 {
		_ = s.out.text("Older: /older | Recent: /result\n")
	} else {
		_ = s.out.text("End of saved history. /result returns to recent results.\n")
	}
	return nil
}

type choice struct{ value, label string }

func (s *session) choose(ctx context.Context, title string, rows []choice, current ...string) (string, error) {
	if s.terminal != nil {
		value := ""
		if len(current) != 0 {
			value = current[0]
		}
		return s.chooseTerminal(ctx, title, rows, value)
	}
	visible := rows
	for {
		_ = s.out.text(title + "\n")
		for i, row := range visible {
			_ = s.out.text(fmt.Sprintf("  %d. %s\n", i+1, row.label))
		}
		_ = s.out.text("  0. Back | Type a number or search text; Enter clears search\nChoose > ")
		answer := s.input.next(ctx)
		if answer.err != nil {
			return "", answer.err
		}
		text := strings.TrimSpace(answer.text)
		if text == "0" || text == "/back" {
			return "", nil
		}
		if number, err := strconv.Atoi(text); err == nil {
			if number > 0 && number <= len(visible) {
				return visible[number-1].value, nil
			}
			_ = s.out.text("Choose a listed number, or 0 to go back.\n")
			continue
		}
		visible = nil
		for _, row := range rows {
			if strings.Contains(strings.ToLower(row.label), strings.ToLower(text)) {
				visible = append(visible, row)
			}
		}
		if len(visible) == 0 {
			_ = s.out.text("No matches. Change the search or press Enter for all choices.\n")
		}
	}
}

func (s *session) selectSetting(ctx context.Context, field string) error {
	if field == "/models" || field == "/accounts" {
		if _, err := s.app.LinkedConnections(ctx); err != nil {
			return connectionProblem(err)
		}
	}
	rows := []choice{{"auto", "auto - choose within authorized, supported routes"}}
	current := ""
	switch field {
	case "/models":
		current = s.selection.Model
		for _, model := range s.app.Models() {
			rows = append(rows, choice{model.ID, model.ID + " - " + model.Label + " | " + model.Runtime + " | effort " + modelEfforts(model) + " | " + model.Reason})
		}
	case "/accounts":
		current = s.selection.Account
		for _, account := range s.app.Accounts() {
			rows = append(rows, choice{account.ID, account.ID + " - " + account.Label + " | " + account.Runtime + " | " + account.Status})
		}
		rows = append(rows, choice{"__connect__", "Connect another account - native sign-in"})
	case "/effort":
		current = s.selection.Effort
		for _, effort := range []string{"low", "medium", "high", "max"} {
			rows = append(rows, choice{effort, effort})
		}
	case "/permission":
		current = s.selection.Permission
		rows = []choice{{"read-only", "read-only - require an enforceable read-only route"}, {"workspace-write", "workspace-write - permit supported workspace changes"}, {"sandbox", "sandbox - full local computer access"}, {"unrestricted", "unrestricted - request broader authority explicitly"}}
	}
	value, err := s.choose(ctx, "Choose "+strings.TrimPrefix(field, "/")+" for the next submission", rows, current)
	if err != nil || value == "" {
		return err
	}
	if field == "/accounts" && value == "__connect__" {
		return s.connectionAction(ctx, "/login", "")
	}
	switch field {
	case "/models":
		s.selection.Model = value
		_ = s.out.text("Model choice recorded. Runtime changes may change native tools, skills and context.\nNative follow-ups start fresh sessions; prior prompts/results stay in local history.\n/explain shows the route before /send; native resume is unavailable.\n")
	case "/accounts":
		s.selection.Account = value
		_ = s.out.text("Account choice recorded. Auto never broadens the authorized account set.\n")
	case "/effort":
		s.selection.Effort = value
		_ = s.out.text("Effort changes reasoning intent, not permissions. Unsupported choices are rejected.\n")
	case "/permission":
		s.selection.Permission = value
		if value == "sandbox" {
			_ = s.out.text("Sandbox selected: full local computer access using the connected agent's tools.\n")
		} else {
			_ = s.out.text("Permission request recorded. A runtime must enforce it before work starts.\n")
		}
	}
	if s.task != nil && s.task.Draft != "" {
		if err := s.saveDraft(ctx, s.task.Draft); err != nil {
			return err
		}
	}
	return s.out.notice(strings.TrimPrefix(field, "/")+" selected: "+value, toneSuccess)
}
