// Package app owns execution, durable task state and independent UI observation.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/adapters/claude"
	"ucli.local/ucli/internal/adapters/codex"
	"ucli.local/ucli/internal/adapters/muse"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
	"ucli.local/ucli/internal/state"
	"ucli.local/ucli/internal/workspace"
)

type Options struct {
	Input, TaskID, CommandID, CWD string
	ExpectedRevision              *int
	Selection                     core.Selection
	Demo                          bool
}
type App struct {
	Store *state.Store
	Home  string
	owner core.Owner
	host  *executionHost
}

func DefaultHome() (string, error) {
	if home := os.Getenv("UCLI_HOME"); home != "" {
		return filepath.Abs(home)
	}
	if runtime.GOOS == "linux" {
		if base := os.Getenv("XDG_STATE_HOME"); base != "" && filepath.IsAbs(base) {
			return filepath.Join(base, "ucli"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "ucli"), nil
	}
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "ucli"), nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ucli"), nil
}
func Open(home string) (*App, error) {
	var err error
	if home == "" {
		home, err = DefaultHome()
		if err != nil {
			return nil, core.Problem("state_unavailable", err.Error(), 9)
		}
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	store, err := state.Open(home)
	if err != nil {
		return nil, err
	}
	a := &App{Store: store, Home: home, owner: core.Owner{ID: core.NewID("owner"), PID: os.Getpid(), StartedAt: time.Now().UTC()}, host: newExecutionHost()}
	return a, nil
}
func (a *App) Tasks(ctx context.Context) ([]core.Task, error) { return a.Store.ListTasks(ctx, 100, 0) }
func (a *App) TaskSummaries(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
	return a.Store.ListTaskSummaries(ctx, query, cursor, limit)
}

func (a *App) Backup(ctx context.Context, destination string) (state.BackupReceipt, error) {
	if destination == "" {
		return state.BackupReceipt{}, core.Problem("invalid_input", "A new backup filename is required.", 2)
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return state.BackupReceipt{}, core.Problem("invalid_input", "The backup filename could not be resolved.", 2)
	}
	return a.Store.Backup(ctx, abs)
}

// RecentDraft restores only the newest draft for this execution directory. It
// does not load unrelated drafts or change the task's displayed editing revision.
func (a *App) RecentDraft(ctx context.Context, cwd string) (*core.Task, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, core.Problem("invalid_workspace", "The current directory could not be resolved.", 2)
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, core.Problem("invalid_workspace", "The draft directory could not be resolved.", 2)
	}
	return a.Store.RecentDraft(ctx, abs)
}
func (a *App) Task(ctx context.Context, id string) (core.Task, []core.Attempt, error) {
	task, err := a.Store.GetTask(ctx, id)
	if err != nil {
		return task, nil, err
	}
	attempts, err := a.Store.Attempts(ctx, id)
	return task, attempts, err
}

// TaskPage reads recent or older evidence without loading the whole transcript.
// It is read-only and must not refresh a client's displayed editing revision.
func (a *App) TaskPage(ctx context.Context, id string, beforeRevision, limit int) (core.Task, state.AttemptPage, error) {
	task, err := a.Store.GetTask(ctx, id)
	if err != nil {
		return task, state.AttemptPage{}, err
	}
	page, err := a.Store.ListAttempts(ctx, id, beforeRevision, limit)
	return task, page, err
}
func (a *App) resolveWorkspace(ctx context.Context, taskID, cwd string) (core.Workspace, core.Task, error) {
	var task core.Task
	if taskID != "" {
		var err error
		task, err = a.Store.GetTask(ctx, taskID)
		if err != nil {
			return core.Workspace{}, task, err
		}
		if cwd == "" {
			cwd = task.Workspace.Dir
		}
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return core.Workspace{}, task, err
		}
	}
	w, err := workspace.Resolve(cwd)
	if err != nil {
		return w, task, core.Problem("invalid_workspace", err.Error(), 2)
	}
	if taskID != "" && task.Workspace.ID != w.ID {
		return w, task, core.Problem("workspace_changed", "This task belongs to another workspace. Start a new task for this directory.", 5)
	}
	return w, task, nil
}
func (a *App) Draft(ctx context.Context, taskID, text, cwd string, selection core.Selection) (core.Task, error) {
	return a.DraftAt(ctx, taskID, text, cwd, selection, nil)
}

// DraftAt guards the revision the client actually displayed. A conflicting edit
// is preserved as a separate draft, never applied to newer task context or sent.
func (a *App) DraftAt(ctx context.Context, taskID, text, cwd string, selection core.Selection, expectedRevision *int) (core.Task, error) {
	if len(text) > core.MaxInputBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return core.Task{}, core.Problem("invalid_input", "Draft must be valid UTF-8 without NUL, at most 1 MiB.", 2)
	}
	w, task, err := a.resolveWorkspace(ctx, taskID, cwd)
	if err != nil {
		return task, err
	}
	selection = inherit(task.Selection, selection)
	if err := core.ValidateSelection(selection); err != nil {
		return task, err
	}
	task.ID = taskID
	task.Workspace = w
	task.Draft = text
	task.Selection = selection
	if task.Title == "" {
		task.Title = title(text)
	}
	revision := task.Revision
	if expectedRevision != nil {
		revision = *expectedRevision
	}
	saved, err := a.Store.SaveDraft(ctx, task, revision)
	if err != nil {
		return saved, a.preserveConflict(ctx, task, err)
	}
	return saved, nil
}

func (a *App) preserveConflict(ctx context.Context, task core.Task, err error) error {
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "revision_conflict" {
		return err
	}
	if task.Draft == "" {
		return err
	}
	recovery := core.Task{Title: "Recovered draft for " + task.ID, Workspace: task.Workspace, Draft: task.Draft, Selection: task.Selection}
	saved, saveErr := a.Store.SaveDraft(ctx, recovery, 0)
	if saveErr != nil {
		return fmt.Errorf("task changed and recovery draft could not be saved: %w", saveErr)
	}
	copy := *p
	copy.TaskID = saved.ID
	copy.Message += " Your submitted text is saved separately as draft " + saved.ID + "; review it before sending."
	return &copy
}

func inherit(base, explicit core.Selection) core.Selection {
	base = base.WithDefaults()
	if explicit.Model != "" {
		base.Model = explicit.Model
	}
	if explicit.Account != "" {
		base.Account = explicit.Account
	}
	if explicit.Effort != "" {
		base.Effort = explicit.Effort
	}
	if explicit.Permission != "" {
		base.Permission = explicit.Permission
	}
	return base
}
func title(input string) string {
	r := []rune(strings.Join(strings.Fields(input), " "))
	if len(r) > 80 {
		r = append(r[:77], '.', '.', '.')
	}
	return string(r)
}

func (a *App) prepareRun(ctx context.Context, opt Options, reserve func() error) (preparedRun, error) {
	if err := core.ValidateInput(opt.Input); err != nil {
		return preparedRun{}, err
	}
	w, task, err := a.resolveWorkspace(ctx, opt.TaskID, opt.CWD)
	if err != nil {
		return preparedRun{}, err
	}
	requested := inherit(task.Selection, opt.Selection)
	if err := core.ValidateSelection(requested); err != nil {
		return preparedRun{}, err
	}
	revision := task.Revision
	if opt.ExpectedRevision != nil {
		revision = *opt.ExpectedRevision
	}
	commandID := opt.CommandID
	if commandID == "" {
		commandID = core.NewID("command")
	}
	cmd := core.RunCommand{ID: commandID, TaskID: opt.TaskID, ExpectedRevision: revision, Input: opt.Input, Requested: requested, Workspace: w, Owner: a.owner}
	if opt.CommandID != "" {
		previous, hash, lookupErr := a.Store.GetCommand(ctx, commandID)
		if lookupErr == nil {
			cmd.Requested = inherit(previous.Requested, opt.Selection)
			if opt.TaskID != "" && opt.CWD == "" {
				cmd.Workspace = previous.Workspace
			}
			if opt.ExpectedRevision == nil && opt.TaskID != "" {
				cmd.ExpectedRevision = previous.Revision - 1
			}
			if cmd.IntentHash() != hash {
				return preparedRun{}, core.Problem("command_conflict", "This command ID already belongs to different input/settings. Use a new command ID for new work.", 5)
			}
			return preparedRun{attempt: previous, replay: true}, nil
		}
		var problem *core.Error
		if !errors.As(lookupErr, &problem) || problem.Code != "not_found" {
			return preparedRun{}, lookupErr
		}
	}
	// Replay is resolved before reserving a fresh worker or probing providers.
	if err := reserve(); err != nil {
		return preparedRun{}, err
	}
	var route core.Route
	var executable string
	var binding *adapters.NativeBinding
	if opt.Demo {
		route, executable, err = a.resolveRoute(ctx, opt, true)
	} else {
		route, executable, binding, err = a.resolveNativeRoute(ctx, opt, true)
	}
	if err != nil {
		draft, saveErr := a.DraftAt(ctx, opt.TaskID, opt.Input, opt.CWD, requested, opt.ExpectedRevision)
		if saveErr != nil {
			return preparedRun{}, saveErr
		}
		var problem *core.Error
		if errors.As(err, &problem) {
			copy := *problem
			copy.TaskID = draft.ID
			copy.Acceptance = "not-dispatched"
			copy.SideEffects = "none"
			copy.Message += " Your input is saved as task " + draft.ID + "."
			return preparedRun{attempt: core.Attempt{TaskID: draft.ID}}, &copy
		}
		return preparedRun{attempt: core.Attempt{TaskID: draft.ID}}, err
	}
	cmd.Effective = route
	attempt, fresh, err := a.Store.Accept(ctx, cmd)
	if err != nil {
		recovery := task
		recovery.ID = opt.TaskID
		recovery.Draft = opt.Input
		recovery.Workspace = w
		recovery.Selection = requested
		return preparedRun{attempt: attempt}, a.preserveConflict(ctx, recovery, err)
	}
	if !fresh {
		return preparedRun{attempt: attempt, replay: true}, nil
	}
	driver := adapters.Driver(demoDriver{})
	if route.Mode == "echo" {
		driver = muse.Driver{}
	}
	if route.Mode == "native" {
		switch route.Runtime {
		case "codex":
			driver = codex.Driver{}
		case "claude":
			driver = claude.Driver{}
		}
	}
	return preparedRun{attempt: attempt, executable: executable, driver: driver, binding: binding}, nil
}

func resultError(attempt core.Attempt) error {
	if attempt.Failure != nil {
		if attempt.State != "failed" || core.ValidateFailureClass(attempt.Failure) != nil {
			p := core.Problem("invalid_stored_failure", "The saved failure classification is invalid; preserve this attempt for inspection before retrying.", 9)
			p.TaskID = attempt.TaskID
			return p
		}
		f := attempt.Failure
		p := core.Problem(f.Code, attempt.Error, f.Exit)
		p.TaskID, p.Origin, p.Acceptance, p.SideEffects = attempt.TaskID, f.Origin, f.Acceptance, f.SideEffects
		return p
	}
	switch attempt.State {
	case "complete":
		return nil
	case "interrupted":
		if attempt.StopCause == "deadline" {
			return core.Problem("timeout", "This attempt reached its deadline and was confirmed stopped. Saved partial output remains available.", 124)
		}
		return core.Problem("interrupted", "This attempt was interrupted. Inspect its result before starting new work.", 130)
	case "unknown", "accepted", "running":
		return core.Problem("outcome_unknown", "This command already has an unsettled attempt; inspect or reconcile it before new work.", 7)
	default:
		return core.Problem("task_failed", "The stored attempt failed: "+attempt.Error, 8)
	}
}
func (a *App) Recover(ctx context.Context, taskID string) (core.Attempt, error) {
	_, page, err := a.TaskPage(ctx, taskID, 0, 1)
	if err != nil {
		return core.Attempt{}, err
	}
	if len(page.Attempts) == 0 {
		return core.Attempt{}, core.Problem("not_found", "This task has no attempt to recover.", 2)
	}
	attempt := page.Attempts[len(page.Attempts)-1]
	if attempt.Effective.Mode != "demo" {
		return attempt, core.Problem("reconciliation_required", "Remote outcomes require provider-specific inspection; automatic recovery is unavailable.", 7)
	}
	if attempt.State != "running" && attempt.State != "accepted" && attempt.State != "unknown" {
		return attempt, core.Problem("invalid_state", "This attempt is already settled. Inspect its result or follow up with new work.", 5)
	}
	alive, err := process.Alive(attempt.Owner.PID)
	if err != nil || alive {
		return attempt, core.Problem("owner_active", "The recorded owner may still be active; recovery cannot take its task. A PID alone does not establish ownership.", 5)
	}
	// This route has no remote/model effects and the recorded process no longer exists.
	return a.Store.Reconcile(ctx, attempt.ID, attempt.Owner.ID, true)
}
