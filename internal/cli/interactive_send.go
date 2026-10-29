package cli

import (
	"context"
	"strings"

	"ucli.local/ucli/internal/core"
)

// One Enter expresses one submission. Persist the full composition first so
// routing, busy-workspace and other pre-acceptance failures cannot lose it.
func (s *session) submitPrompt(ctx context.Context, text string, replace bool) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if s.recoveryID != "" {
		_ = s.out.text("Copy these additional edits before switching sessions:\n" + text + "\n")
		return core.Problem("draft_conflict", "Your unsent message is safe. Open /tasks or /new before sending another message.", 5)
	}
	if !replace && s.task != nil && s.task.Draft != "" {
		text = s.task.Draft + "\n" + text
	}
	if err := s.storeDraft(ctx, text, false); err != nil {
		return err
	}
	return s.sendDraft(ctx)
}

// The older /send action uses exactly the same submission boundary as Enter.
func (s *session) sendDraft(ctx context.Context) error {
	if s.task == nil || strings.TrimSpace(s.task.Draft) == "" {
		return usageError("type a prompt and press Enter to send it")
	}
	submitted := s.task.Draft
	s.historyOpen, s.historyNext = false, 0
	if s.terminal != nil && s.observeCtx != nil {
		if s.currentExecution() != nil {
			return core.Problem("task_busy", "This task is still working. Your next prompt is saved, not queued or sent. Use /sessions to watch, /stop to request stop, or /new for another task.", 5)
		}
		if !permissionSupported(s.selection.WithDefaults().Permission, s.demo) {
			// Restored unsupported pins need an explicit choice. Even a confirmed
			// choice returns to the draft; only a later Enter may dispatch it.
			return s.selectPermission(ctx)
		}
		_ = s.out.prompt(submitted)
		return s.startInteractiveExecution(ctx, submitted)
	}
	_ = s.out.prompt(submitted)
	_, runErr := runTask(ctx, s.app, s.options(), s.out)
	if runErr != nil && s.markConflict(runErr, submitted) {
		return runErr
	}
	if err := s.refresh(ctx); err != nil {
		return err
	}
	s.selection = s.task.Selection.WithDefaults()
	if runErr != nil {
		return runErr
	}
	ready := "Ready for your next prompt | /sessions shows progress and results"
	if s.terminal == nil {
		return s.out.text(ready + "\n")
	}
	return s.out.notice(ready, toneSuccess)
}
