package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rivo/uniseg"
	"ucli.local/ucli/internal/workflow"
)

func viewWorkflow(ctx context.Context, p invocation, in io.Reader, o *output, existing *terminalUI) int {
	if err := inspectWorkflow(ctx, p, in, o, existing); err != nil {
		return o.problem(err)
	}
	return 0
}

func inspectWorkflow(ctx context.Context, p invocation, in io.Reader, o *output, existing *terminalUI) error {
	spec, plan, err := loadWorkflow(ctx, p, in)
	if err != nil {
		return workflowViewError(err)
	}
	if o.json {
		return o.data("workflow_view", struct {
			Spec       workflow.Spec `json:"spec"`
			Plan       workflow.Plan `json:"plan"`
			Offline    bool          `json:"offline"`
			Dispatched bool          `json:"dispatched"`
		}{spec, plan, true, false})
	}
	terminal := existing
	if terminal == nil && !p.noInput && !p.set["plain"] && os.Getenv("TERM") != "dumb" && isTerminal(in) && isTerminal(o.out) {
		terminal = newTerminalUI(in, o.out, o.cancel, !p.set["no-color"] && os.Getenv("NO_COLOR") == "", terminalOptionsFor(p))
		o.terminal = terminal // Run owns closure after draining output.
		o.out, o.errOut = terminal.writer, terminal.writer
		location, _ := os.Getwd()
		if err := terminal.setChrome(terminalChrome{Version: Version, Location: location, Model: "Workflow plan", Context: "Offline | not executed", Panel: true}); err != nil {
			return err
		}
		if err := terminal.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "Workflow plan · Esc back · Ctrl+C close", Dim: true}}, CursorRow: -1}); err != nil {
			return err
		}
	}
	if terminal != nil {
		_ = o.notice("Plan inspection only | offline; no task started", toneInfo)
		_ = o.activity("peek", true)
		defer o.activity("", true)
		err = inspectWorkflowTerminal(ctx, terminal, spec, plan)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return workflowViewError(err)
		}
		return nil
	}
	if err := o.text(fmt.Sprintf("Plan · not executed · %s · %d steps\nDependency-ready: %d · waiting: %d. No tasks were accepted or started.\n", spec.ID, len(spec.Steps), len(plan.Ready), len(plan.Blocked))); err != nil {
		return err
	}
	for _, step := range spec.Steps {
		if err := ctx.Err(); err != nil {
			return workflowViewError(err)
		}
		if err := o.text("\n" + strings.Join(workflowStepText(step, plan), "\n") + "\n"); err != nil {
			return err
		}
	}
	return nil
}

func workflowViewError(err error) error {
	problem := *problemFor(err)
	problem.Acceptance, problem.SideEffects = "not-sent", "none"
	if errors.Is(err, context.Canceled) {
		problem.Message = "Workflow view interrupted. No tasks were accepted or started."
	} else if errors.Is(err, context.DeadlineExceeded) {
		problem.Message = "Workflow view deadline exceeded. No tasks were accepted or started."
	}
	return &problem
}

func workflowStepText(step workflow.Step, plan workflow.Plan) []string {
	selection := step.Selection.WithDefaults()
	dependencies := strings.Join(step.DependsOn, ", ")
	if dependencies == "" {
		dependencies = "none"
	}
	lines := []string{"Step " + step.ID, "Not executed · dependency readiness: " + workflowReadiness(step.ID, plan), "Dependencies: " + dependencies}
	for _, blocked := range plan.Blocked {
		if blocked.ID == step.ID {
			for _, reason := range blocked.Reasons {
				lines = append(lines, reason.Message)
			}
		}
	}
	lines = append(lines, "Requested model: "+selection.Model, "Requested effort: "+selection.Effort,
		"Requested account: "+selection.Account, "Requested permission: "+selection.Permission,
		"No effective route or result exists.", "", "Prompt:")
	return append(lines, strings.Split(step.Prompt, "\n")...)
}

func workflowReadiness(id string, plan workflow.Plan) string {
	for _, ready := range plan.Ready {
		if ready == id {
			return "ready"
		}
	}
	for _, blocked := range plan.Blocked {
		if blocked.ID == id {
			return "waiting"
		}
	}
	return "unknown"
}

type workflowView struct {
	spec                  workflow.Spec
	plan                  workflow.Plan
	query                 textEditor
	focus, top, scroll    int
	detail                bool
	message               string
	cacheStep, cacheWidth int
	cache                 []string
}

func (v *workflowView) matches() []int {
	query := strings.ToLower(v.query.text)
	var matches []int
	for index, step := range v.spec.Steps {
		// Filter metadata only: prompts can total 1 MiB and remain in detail.
		text := step.ID + " " + strings.Join(step.DependsOn, " ") + " " + step.Selection.Model + " " + step.Selection.Account + " " + step.Selection.Effort + " " + step.Selection.Permission
		if strings.Contains(strings.ToLower(text), query) {
			matches = append(matches, index)
		}
	}
	return matches
}

func (v *workflowView) focusPosition(matches []int) int {
	for position, index := range matches {
		if index == v.focus {
			return position
		}
	}
	return -1
}

func inspectWorkflowTerminal(ctx context.Context, terminal pickerTerminal, spec workflow.Spec, plan workflow.Plan) error {
	v := workflowView{spec: spec, plan: plan, cacheStep: -1}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		width, height := 80, 24
		if sized, ok := terminal.(interface{ size() (int, int) }); ok {
			width, height = sized.size()
		}
		if err := terminal.paint(v.frame(width, height)); err != nil {
			return err
		}
		event, err := terminal.next(ctx)
		if err != nil {
			return err
		}
		if event.kind == keyEOF {
			return io.EOF
		}
		if event.kind == keyInterrupt {
			return context.Canceled
		}
		if event.kind == keyEscape || event.kind == keyLeft {
			if v.detail {
				v.detail = false
				continue
			}
			return nil
		}
		if width < 20 || height < 5 {
			continue
		}
		if v.detail {
			switch event.kind {
			case keyUp:
				v.scroll--
			case keyDown:
				v.scroll++
			case keyPageUp:
				v.scroll -= max(1, height-5)
			case keyPageDown:
				v.scroll += max(1, height-5)
			case keyHome:
				v.scroll = 0
			case keyEnd:
				v.scroll = len(v.cache)
			case keyText:
				if event.text == "j" {
					v.scroll++
				} else if event.text == "k" {
					v.scroll--
				}
			}
			continue
		}
		matches := v.matches()
		position := v.focusPosition(matches)
		switch event.kind {
		case keyEnter, keyRight:
			if position >= 0 {
				v.detail = true
				v.scroll = 0
				v.message = ""
			}
		case keyUp, keyDown:
			if len(matches) > 0 {
				if event.kind == keyUp {
					position--
				} else {
					position++
				}
				v.focus = matches[min(max(0, position), len(matches)-1)]
				v.scroll = 0
			}
		case keyText, keyPaste, keyBackspace, keyDelete, keyHome, keyEnd, keyClear, keyKillEnd:
			if event.kind == keyPaste {
				event.text = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(event.text, "\r\n", " "), "\r", " "), "\n", " ")
			}
			candidate := v.query
			if err := candidate.apply(event); err != nil {
				v.message = err.Error()
				continue
			}
			if len(candidate.text) > 256 {
				v.message = "Filter is limited to 256 UTF-8 bytes."
				continue
			}
			v.query = candidate
			v.message = ""
			v.scroll = 0
			matches = v.matches()
			if v.focusPosition(matches) < 0 && len(matches) > 0 {
				v.focus = matches[0]
			}
		}
	}
}

func (v *workflowView) detailLines(width int) []string {
	if v.cacheStep != v.focus || v.cacheWidth != width {
		v.cache = nil
		for _, line := range workflowStepText(v.spec.Steps[v.focus], v.plan) {
			v.cache = append(v.cache, workflowWrap(line, width)...)
		}
		v.cacheStep, v.cacheWidth = v.focus, width
	}
	return v.cache
}

func (v *workflowView) frame(width, height int) terminalFrame {
	budget := max(1, height-1)
	view := terminalFrame{CursorRow: -1}
	add := func(text string, focus, dim bool) {
		view.Lines = append(view.Lines, terminalLine{Text: text, Focused: focus, Dim: dim})
	}
	title := fmt.Sprintf("Plan · not executed · %s · %d steps", v.spec.ID, len(v.spec.Steps))
	if width < 20 || height < 5 {
		add("Plan · not executed", false, true)
		if budget > 1 {
			add("Resize to inspect", false, false)
		}
		if budget > 2 {
			add("Esc back/close", false, true)
		}
		return view
	}
	add(title, false, true)
	matches := v.matches()
	position := v.focusPosition(matches)
	if v.detail {
		add("Step "+v.spec.Steps[v.focus].ID+" · detail", false, false)
	} else {
		query, col := editorLineWindow(v.query.text, v.query.cursor, max(1, width-9))
		view.CursorRow, view.CursorCol = 1, 8+col
		add("Filter: "+query, false, false)
	}
	if height >= 8 {
		text := fmt.Sprintf("%d matching · %d ready / %d waiting · dependency-only", len(matches), len(v.plan.Ready), len(v.plan.Blocked))
		if v.message != "" {
			text = v.message
		}
		add(text, false, true)
	} else if v.message != "" {
		if height >= 6 {
			add(v.message, false, false)
		} else {
			// At five rows retain the candidate and recovery keys; temporarily
			// replace the query's display, preserving its actual text/cursor.
			view.Lines[1].Text = v.message
			view.CursorRow = -1
		}
	}
	rows := max(1, budget-len(view.Lines)-1)
	wide := width >= 70 && height >= 8
	leftWidth := min(28, max(16, width/3))
	detailWidth := max(1, width-1)
	if wide {
		detailWidth = width - leftWidth - 4
	}
	var details []string
	if position >= 0 {
		details = v.detailLines(detailWidth)
	}
	if v.detail {
		v.scroll = min(max(0, v.scroll), max(0, len(details)-rows))
	} else {
		v.scroll = 0
	}
	v.top = pickerWindow(v.top, position, len(matches), rows)
	for row := 0; row < rows; row++ {
		left := ""
		focused := false
		if n := v.top + row; n < len(matches) {
			index := matches[n]
			focused = index == v.focus
			prefix := "  "
			if focused {
				prefix = "> "
			}
			left = prefix + v.spec.Steps[index].ID + " [" + workflowReadiness(v.spec.Steps[index].ID, v.plan) + "]"
		} else if row == 0 && len(matches) == 0 {
			left = "No matches. Edit filter."
		}
		right := ""
		if n := v.scroll + row; n < len(details) {
			right = details[n]
		}
		if wide {
			left = clipCells(inlineText(left), leftWidth)
			add(left+strings.Repeat(" ", max(0, leftWidth-cellWidth(left)))+" │ "+right, focused && !v.detail, false)
		} else if v.detail {
			add(right, false, false)
		} else {
			add(left, focused, false)
		}
	}
	footer := "Enter open · Esc close · Up/Down select · Type filter"
	if v.detail {
		footer = fmt.Sprintf("Esc/Left back · Up/Down scroll · %d-%d/%d", v.scroll+1, min(v.scroll+rows, len(details)), len(details))
	}
	if width < 55 {
		footer = "Enter open · Esc close · Type filter"
		if v.detail {
			footer = "Esc back · Up/Down scroll"
		}
	}
	if v.message != "" && !v.detail {
		footer = "Ctrl+U clear · Esc close · Enter open"
	}
	add(footer, false, true)
	return view
}

func workflowWrap(text string, width int) []string {
	// Input is bounded by the workflow parser. Cache each selected step's wrapped
	// detail; repeated keypresses do not rescan its up-to-1-MiB prompt. A line index
	// would replace this bounded allocation if document limits grow.
	text = inlineText(text)
	var lines []string
	start, end, used := 0, 0, 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		from, to := g.Positions()
		if used+g.Width() > width && from > start {
			lines = append(lines, text[start:from])
			start = from
			used = 0
		}
		used += g.Width()
		end = to
	}
	return append(lines, text[start:end])
}
