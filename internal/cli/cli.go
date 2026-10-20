// Package cli implements the terminal and scripted UCLI surfaces. The app service
// owns task state and execution; this package owns parsing, input and rendering.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

// Run executes one CLI invocation. Help and version never open local state.
func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	p, parseErr := parse(args)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	o := newOutput(out, errOut, p.json, cancel)
	exit := execute(ctx, p, parseErr, in, o)
	if err := o.finish(); err != nil {
		exit = 9
	}
	if o.terminal != nil {
		if err := o.terminal.close(); err != nil {
			if exit == 0 {
				exit = problemFor(err).Exit
			}
		}
	}
	return exit
}

func execute(ctx context.Context, p invocation, parseErr error, in io.Reader, o *output) int {
	if parseErr != nil {
		return o.problem(parseErr)
	}
	if p.help {
		if o.json {
			_ = o.data("help", help(p.command, false))
		} else {
			_ = o.text(help(p.command, false))
		}
		return 0
	}
	if p.version {
		if o.json {
			_ = o.data("version", Version)
		} else {
			_ = o.text("ucli " + Version + "\n")
		}
		return 0
	}
	if p.command == "workflow check" {
		return checkWorkflow(ctx, p, in, o)
	}
	if p.command == "workflow view" {
		return viewWorkflow(ctx, p, in, o, nil)
	}
	if strings.HasPrefix(p.command, "runtime ") || strings.HasPrefix(p.command, "voice ") {
		return optionalCommand(ctx, p, in, o)
	}
	if isConnectionCommand(p.command) {
		return connectionCommand(ctx, p, in, o)
	}
	interactive := p.command == "" && !p.noInput && !p.json && isTerminal(in) && isTerminal(o.out)
	if p.command == "" && !interactive {
		if o.json {
			_ = o.data("help", help("", true))
		} else {
			_ = o.text(help("", true))
		}
		return 0
	}
	if p.prompt == "-" {
		if isTerminal(in) {
			return o.problem(usageError("redirect prompt text into ucli run -, or pass a quoted prompt"))
		}
		b, err := readPrompt(ctx, in)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return o.problem(err)
			}
			return o.problem(core.Problem("input_read", "cannot read prompt: "+err.Error(), 2))
		}
		if len(b) > 1<<20 {
			return o.problem(usageError("prompt exceeds the 1 MiB input limit"))
		}
		p.prompt = string(b)
	}
	if (p.command == "run" || p.command == "tasks followup") && strings.TrimSpace(p.prompt) == "" {
		return o.problem(usageError("prompt is empty; supply the work to do"))
	}
	home, err := invocationHome(p)
	if err != nil {
		return o.problem(err)
	}
	a, err := app.Open(home)
	if err != nil {
		return o.problem(err)
	}
	if interactive {
		exit := interact(ctx, a, p, in, o)
		if err := a.Close(); err != nil {
			return o.problem(err)
		}
		return exit
	}
	exit := dispatch(ctx, a, p, o)
	if err := a.Close(); err != nil {
		return o.problem(err)
	}
	return exit
}

func options(p invocation) app.Options {
	return app.Options{Input: p.prompt, TaskID: p.id, CommandID: p.commandID, CWD: p.cwd, Selection: p.selection, Demo: p.demo}
}

func dispatch(ctx context.Context, a *app.App, p invocation, o *output) int {
	switch p.command {
	case "migrate":
		return migrate(ctx, a, p.file, o)
	case "backup":
		return backup(ctx, a, p.file, o)
	case "run", "tasks followup":
		_, err := runTask(ctx, a, options(p), o)
		if err != nil {
			return o.problem(err)
		}
	case "models":
		if _, err := a.LinkedConnections(ctx); err != nil {
			return o.problem(connectionProblem(err))
		}
		if o.json {
			_ = o.data("models", a.Models())
		} else {
			showModels(a.Models(), o)
		}
	case "accounts":
		if _, err := a.LinkedConnections(ctx); err != nil {
			return o.problem(connectionProblem(err))
		}
		if o.json {
			_ = o.data("accounts", a.Accounts())
		} else {
			showAccounts(a.Accounts(), o)
		}
	case "tasks list":
		page, err := a.TaskSummaries(ctx, p.search, p.cursor, p.pageLimit)
		if err != nil {
			return o.problem(err)
		}
		if o.json {
			_ = o.dataWithPagination("tasks", page.Tasks, taskListPagination{p.pageLimit, p.search, p.cursor, page.NextCursor})
		} else {
			showTaskSummaries(page.Tasks, p.search, p.cursor, page.NextCursor, p.pageLimit, o)
		}
	case "tasks show":
		task, attempts, paging, err := loadHistory(ctx, a, p.id, p.beforeRevision, p.pageLimit)
		if err != nil {
			return o.problem(err)
		}
		showTask(task, attempts, paging, o)
		if !o.json && paging.NextBeforeRevision != 0 {
			_ = o.text(fmt.Sprintf("Older: ucli tasks show %s --limit %d --before-revision %d\n", task.ID, paging.Limit, paging.NextBeforeRevision))
		}
	case "tasks recover":
		attempt, err := a.Recover(ctx, p.id)
		if err != nil {
			return o.problem(err)
		}
		if o.json {
			_ = o.data("recovery", attempt)
		} else {
			_ = o.text(fmt.Sprintf("Task %s: %s\nNo work replayed. Inspect with ucli tasks show %s\n", attempt.TaskID, attempt.State, attempt.TaskID))
		}
	case "explain":
		route, err := a.Explain(ctx, options(p))
		if err != nil {
			return o.problem(err)
		}
		if o.json {
			_ = o.data("route", route)
		} else {
			_ = o.text(routeText(route))
		}
	case "doctor":
		linked, connectionErr := a.LinkedConnections(ctx)
		if connectionErr != nil {
			return o.problem(connectionProblem(connectionErr))
		}
		if err := a.Store.CheckIntegrity(ctx); err != nil {
			return o.problem(err)
		}
		schema, err := a.Store.SchemaVersion(ctx)
		if err != nil {
			return o.problem(err)
		}
		models := a.Models()
		localEcho := "unavailable; no native echo in the local catalog"
		for _, model := range models {
			if model.ID == "muse/echo" {
				localEcho = "unavailable: " + model.Reason
				if model.Available {
					localEcho = "pinned Muse executable found; hash verified at launch; no AI or login"
				}
			}
		}
		providerStatus := "unadmitted"
		if len(linked) > 0 {
			providerStatus = "linked"
		}
		if o.json {
			_ = o.data("doctor", struct {
				Version    string         `json:"build_version"`
				Schema     int            `json:"state_schema"`
				LocalState string         `json:"local_state"`
				Integrity  string         `json:"integrity"`
				Providers  string         `json:"managed_providers"`
				LocalEcho  string         `json:"local_echo"`
				Probe      string         `json:"probe"`
				Models     []core.Model   `json:"models"`
				Accounts   []core.Account `json:"accounts"`
			}{Version, schema, "opened", "ok", providerStatus, localEcho, "local integrity and cached connection metadata; no live provider probes", models, a.Accounts()})
		} else {
			_ = o.text(fmt.Sprintf("UCLI %s\nLocal state: opened | schema %d\nFull integrity check: passed\nManaged providers: %s\nLocal echo: %s\nNo live authentication/quota probes. Use accounts status to refresh sign-in.\nTry: ucli login codex | ucli run --demo \"Try a task\"\n", Version, schema, providerStatus, localEcho))
		}
	}
	return 0
}

func runTask(ctx context.Context, a *app.App, opt app.Options, o *output) (core.Attempt, error) {
	if !o.json && opt.Demo {
		_ = o.progress("DEMO - local simulation or explicit native echo; no AI or real account use\nForeground task; detach is unavailable\n")
	}
	if !o.json {
		if route, err := a.Explain(ctx, opt); err == nil {
			_ = o.progress("Planned route (confirmed in the saved receipt):\n" + routeText(route))
		}
	}
	var hadText, hadWarning atomic.Bool
	_ = o.notice("Preparing your prompt | Ctrl+C twice to interrupt and exit", toneInfo)
	_ = o.activity("working", false)
	completed := false
	defer func() {
		if !completed {
			_ = o.activity("", true)
		}
	}()
	attempt, err := a.Run(ctx, opt, func(event core.Event) error {
		if event.Type == "message_delta" && event.Text != "" {
			hadText.Store(true)
		}
		if event.Type == "warning" && event.Text != "" {
			hadWarning.Store(true)
		}
		return o.event(event)
	})
	if !o.json && !hadWarning.Load() {
		// Command replay returns the durable receipt without replaying its events.
		for _, warning := range attempt.Warnings {
			_ = o.progress("Warning: " + warning + "\n")
		}
	}
	if o.json && attempt.ID != "" {
		_ = o.data("attempt", attempt)
	}
	if err != nil {
		if attempt.TaskID != "" {
			problem := problemFor(err)
			copy := *problem
			if copy.TaskID == "" {
				copy.TaskID = attempt.TaskID
			}
			err = &copy
		}
		return attempt, err
	}
	if !o.json {
		if !hadText.Load() && attempt.Output != "" {
			_ = o.text(attempt.Output)
		}
		if hadText.Load() || attempt.Output != "" {
			_ = o.text("\n")
		}
		_ = o.progress(fmt.Sprintf("Task %s | revision %d | %s (saved)\n", attempt.TaskID, attempt.Revision, attempt.State))
		showRoute(attempt.Effective, o)
		_ = o.notice("Complete | result saved | enter a follow-up or choose /new", toneSuccess)
		if attempt.State == "complete" && !hadWarning.Load() && len(attempt.Warnings) == 0 {
			_ = o.activity("done", false)
			completed = true
		}
	}
	return attempt, nil
}

// Reader ownership stays with the caller. Cancellation can leave one blocked
// read until that reader closes; it never holds the application/store open.
func readPrompt(ctx context.Context, reader io.Reader) ([]byte, error) {
	type result struct {
		bytes []byte
		err   error
	}
	done := make(chan result, 1)
	go func() { b, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1)); done <- result{b, err} }()
	select {
	case r := <-done:
		return r.bytes, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func showRoute(route core.Route, o *output) {
	_ = o.progress(routeText(route))
}

func routeText(route core.Route) string {
	text := fmt.Sprintf("Effective: %s | effort %s | account %s | permission %s\nRuntime: %s | mode %s\n", route.Model, route.Effort, route.Account, route.Permission, route.Runtime, route.Mode)
	for _, reason := range route.Reasons {
		text += "  " + reason + "\n"
	}
	return text
}

func showModels(models []core.Model, o *output) {
	_ = o.text("Models (local catalog; availability is not a live provider check)\n")
	for _, m := range models {
		status := "unavailable"
		if m.Available {
			status = "available"
		}
		_ = o.text(fmt.Sprintf("  %s | %s | runtime %s | effort %s\n    %s; %s\n", m.ID, m.Label, m.Runtime, modelEfforts(m), status, m.Reason))
	}
	_ = o.text("Choose with ucli run --demo --model MODEL PROMPT; simulation or native echo only, no AI\n")
}

func modelEfforts(model core.Model) string {
	if len(model.Efforts) == 0 {
		return "none"
	}
	return strings.Join(model.Efforts, ", ")
}

func showAccounts(accounts []core.Account, o *output) {
	_ = o.text("Accounts (local catalog; quota and real identity remain unverified)\n")
	for _, account := range accounts {
		_ = o.text(fmt.Sprintf("  %s | %s | runtime %s | auth %s | %s\n", account.ID, account.Label, account.Runtime, account.AuthMode, account.Status))
	}
	_ = o.text("Auto stays within authorized eligibility; it never expands to other accounts\n")
}

type historyPagination struct {
	Limit              int `json:"limit"`
	BeforeRevision     int `json:"before_revision"`
	NextBeforeRevision int `json:"next_before_revision"`
}

// A few individually valid results can exceed the store's aggregate page bound.
// Reduce only on that explicit error; keep full attempts and the same cursor.
func loadHistory(ctx context.Context, a *app.App, id string, beforeRevision, limit int) (core.Task, []core.Attempt, historyPagination, error) {
	for {
		task, page, err := a.TaskPage(ctx, id, beforeRevision, limit)
		if err == nil {
			return task, page.Attempts, historyPagination{limit, beforeRevision, page.NextBeforeRevision}, nil
		}
		var problem *core.Error
		if limit <= 1 || !errors.As(err, &problem) || problem.Code != "history_too_large" {
			return core.Task{}, nil, historyPagination{}, err
		}
		limit = max(1, limit/2)
	}
}

func showTask(task core.Task, attempts []core.Attempt, paging historyPagination, o *output) {
	if o.json {
		_ = o.data("task", struct {
			Task       core.Task         `json:"task"`
			Attempts   []core.Attempt    `json:"attempts"`
			Pagination historyPagination `json:"pagination"`
		}{task, attempts, paging})
		return
	}
	_ = o.text(fmt.Sprintf("Task %s | %s | revision %d\nWorkspace: %s\n", task.ID, task.Status, task.Revision, task.Workspace.Dir))
	if task.Draft != "" {
		_ = o.text("Saved draft (unsent):\n" + task.Draft + "\n")
	}
	if len(attempts) == 0 {
		if paging.BeforeRevision == 0 {
			_ = o.text("No attempt has run.\n")
		} else {
			_ = o.text(fmt.Sprintf("No attempts before revision %d.\n", paging.BeforeRevision))
		}
	} else {
		_ = o.text(fmt.Sprintf("Showing %d saved attempts, oldest to newest on this page.\n", len(attempts)))
	}
	for _, attempt := range attempts {
		_ = o.text(fmt.Sprintf("Attempt %s | revision %d | %s\nInput: %s\n", attempt.ID, attempt.Revision, attempt.State, attempt.Input))
		if attempt.Output != "" {
			_ = o.text(attempt.Output + "\n")
		}
		if attempt.Error != "" {
			_ = o.text("Outcome: " + attempt.Error + "\n")
		}
		for _, warning := range attempt.Warnings {
			_ = o.text("Warning: " + warning + "\n")
		}
	}
}
