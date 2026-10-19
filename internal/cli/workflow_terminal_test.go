package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workflow"
)

type workflowTestTerminal struct {
	width, height             int
	events                    []keyEvent
	frames                    []terminalFrame
	resizeWidth, resizeHeight int
}

func (t *workflowTestTerminal) size() (int, int) { return t.width, t.height }
func (t *workflowTestTerminal) paint(frame terminalFrame) error {
	t.frames = append(t.frames, frame)
	return nil
}
func (t *workflowTestTerminal) next(ctx context.Context) (keyEvent, error) {
	if err := ctx.Err(); err != nil {
		return keyEvent{}, err
	}
	if len(t.events) == 0 {
		return keyEvent{}, io.EOF
	}
	event := t.events[0]
	t.events = t.events[1:]
	if event.kind == keyResize {
		t.width, t.height = t.resizeWidth, t.resizeHeight
	}
	return event, nil
}
func workflowFixture(t *testing.T) (workflow.Spec, workflow.Plan) {
	t.Helper()
	spec, plan, err := loadWorkflow(context.Background(), invocation{file: "-"}, strings.NewReader(offlineWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	return spec, plan
}

func TestWorkflowViewMainLoopPreservesPlanAndFilter(t *testing.T) {
	spec, plan := workflowFixture(t)
	before, _ := json.Marshal(spec)
	terminal := &workflowTestTerminal{width: 120, height: 28, events: []keyEvent{
		{kind: keyUp}, {kind: keyDown}, {kind: keyEnter}, {kind: keyEscape},
		{kind: keyText, text: "review"}, {kind: keyRight}, {kind: keyLeft},
		{kind: keyEscape},
	}}
	if err := inspectWorkflowTerminal(context.Background(), terminal, spec, plan); err != nil {
		t.Fatal(err)
	}
	frames := terminal.frames
	if len(frames) != 8 {
		t.Fatalf("frames=%d", len(frames))
	}
	if !strings.Contains(terminalPickerText(frames[0]), "> review") || !strings.Contains(terminalPickerText(frames[1]), "> review") {
		t.Fatal("Up did not clamp at first step")
	}
	for _, index := range []int{2, 4} {
		text := terminalPickerText(frames[index])
		if !strings.Contains(text, "> build") || !strings.Contains(text, "Dependencies: inspect") {
			t.Fatalf("focus/preview lost: %s", text)
		}
	}
	if !strings.Contains(terminalPickerText(frames[3]), "Step build · detail") {
		t.Fatal("Enter did not open selected detail")
	}
	if !strings.Contains(terminalPickerText(frames[7]), "Filter: review") || !strings.Contains(terminalPickerText(frames[7]), "> review") {
		t.Fatal("back discarded query or focus")
	}
	after, _ := json.Marshal(spec)
	if !bytes.Equal(before, after) {
		t.Fatal("inspection mutated specification")
	}
	for _, frame := range frames {
		text := terminalPickerText(frame)
		if !strings.Contains(text, "Plan · not executed") || strings.Contains(text, "pause") || strings.Contains(text, "retry") {
			t.Fatalf("untruthful execution control: %s", text)
		}
	}
}

func TestWorkflowViewNarrowResizeScrollAndRecovery(t *testing.T) {
	spec, plan := workflowFixture(t)
	spec.Steps[0].Prompt = strings.Repeat("A wide Unicode line 中 e\u0301\n", 100) + "LAST PROMPT LINE"
	terminal := &workflowTestTerminal{width: 60, height: 20, resizeWidth: 120, resizeHeight: 28, events: []keyEvent{
		{kind: keyEnter}, {kind: keyPageDown}, {kind: keyEnd}, {kind: keyDown},
		{kind: keyResize}, {kind: keyEscape}, {kind: keyText, text: "unmatched"},
		{kind: keyEnter}, {kind: keyClear}, {kind: keyDown}, {kind: keyEscape},
	}}
	if err := inspectWorkflowTerminal(context.Background(), terminal, spec, plan); err != nil {
		t.Fatal(err)
	}
	f := terminal.frames
	if strings.Contains(terminalPickerText(f[0]), " │ ") || !strings.Contains(terminalPickerText(f[1]), "Step review · detail") {
		t.Fatal("narrow drilldown was not stacked")
	}
	if reflect.DeepEqual(f[1], f[2]) {
		t.Fatal("page scroll did not move content")
	}
	if !strings.Contains(terminalPickerText(f[3]), "LAST PROMPT LINE") || !reflect.DeepEqual(f[3], f[4]) {
		t.Fatal("End/down did not clamp to actual detail end")
	}
	if !strings.Contains(terminalPickerText(f[5]), " │ ") || !strings.Contains(terminalPickerText(f[5]), "Step review · detail") {
		t.Fatal("resize lost selected detail")
	}
	for _, index := range []int{7, 8} {
		if !strings.Contains(terminalPickerText(f[index]), "No matches") {
			t.Fatal("Enter selected an invisible stale match")
		}
	}
	if !strings.Contains(terminalPickerText(f[9]), "> review") {
		t.Fatal("clear did not recover filter")
	}
}

func TestWorkflowViewBoundsLiteralPasteAndClosedInput(t *testing.T) {
	spec, plan := workflowFixture(t)
	for _, size := range [][2]int{{60, 5}, {80, 8}, {120, 28}, {16, 4}} {
		terminal := &workflowTestTerminal{width: size[0], height: size[1], events: []keyEvent{{kind: keyEnter}, {kind: keyEscape}, {kind: keyEscape}}}
		if err := inspectWorkflowTerminal(context.Background(), terminal, spec, plan); err != nil {
			t.Fatal(err)
		}
		for _, frame := range terminal.frames {
			if len(frame.Lines) > size[1]-1 {
				t.Fatalf("height exceeded %v: %+v", size, frame)
			}
			if !strings.Contains(terminalPickerText(frame), "Esc") {
				t.Fatal("Back/close hint unavailable")
			}
		}
	}
	terminal := &workflowTestTerminal{width: 60, height: 20, events: []keyEvent{
		{kind: keyPaste, text: "/send\n/exit"}, {kind: keyEnter}, {kind: keyClear},
		{kind: keyText, text: strings.Repeat("x", 257)}, {kind: keyEscape},
	}}
	if err := inspectWorkflowTerminal(context.Background(), terminal, spec, plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(terminalPickerText(terminal.frames[1]), "Filter: /send /exit") || !strings.Contains(terminalPickerText(terminal.frames[2]), "No matches") {
		t.Fatal("paste escaped literal search")
	}
	if !strings.Contains(terminalPickerText(terminal.frames[4]), "limited to 256") {
		t.Fatal("oversized filter was not recoverable")
	}
	for _, tc := range []struct {
		event keyEvent
		want  error
	}{{keyEvent{kind: keyEOF}, io.EOF}, {keyEvent{kind: keyInterrupt}, context.Canceled}} {
		terminal := &workflowTestTerminal{width: 60, height: 20, events: []keyEvent{tc.event}}
		if err := inspectWorkflowTerminal(context.Background(), terminal, spec, plan); !errors.Is(err, tc.want) {
			t.Fatalf("close=%v", err)
		}
	}
}

func TestWorkflowViewOfflinePlainJSONAndInvalidInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(file, []byte(offlineWorkflow), 0600); err != nil {
		t.Fatal(err)
	}
	for _, jsonMode := range []bool{false, true} {
		var out, diagnostic bytes.Buffer
		o := newOutput(&out, &diagnostic, jsonMode, func() {})
		p := invocation{file: file, home: filepath.Join(dir, "must-not-exist"), noInput: true, set: map[string]bool{}}
		code := viewWorkflow(context.Background(), p, strings.NewReader(""), o, nil)
		if err := o.finish(); err != nil {
			t.Fatal(err)
		}
		if code != 0 || diagnostic.Len() != 0 {
			t.Fatalf("view %d: %s", code, diagnostic.String())
		}
		if jsonMode {
			rows := jsonLines(t, out.String())
			var value struct {
				Spec                workflow.Spec `json:"spec"`
				Plan                workflow.Plan `json:"plan"`
				Offline, Dispatched bool
			}
			if len(rows) != 1 || rows[0].Type != "workflow_view" || json.Unmarshal(rows[0].Data, &value) != nil || !value.Offline || value.Dispatched || len(value.Spec.Steps) != 4 || len(value.Plan.Ready) != 2 {
				t.Fatalf("invalid offline receipt: %s", out.String())
			}
		} else if !strings.Contains(out.String(), "Plan · not executed") || !strings.Contains(out.String(), "SECRET review text") || !strings.Contains(out.String(), "Requested permission: read-only") {
			t.Fatal(out.String())
		}
		if _, err := os.Stat(p.home); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("viewer opened state", err)
		}
	}
	for _, input := range []string{"{", strings.Repeat(" ", workflow.MaxDocumentBytes+1), `{"version":1,"id":"cyclic","steps":[{"id":"a","prompt":"x","depends_on":["a"]}]}`} {
		var out, diagnostic bytes.Buffer
		o := newOutput(&out, &diagnostic, true, func() {})
		err := inspectWorkflow(context.Background(), invocation{file: "-"}, strings.NewReader(input), o, nil)
		var problem *core.Error
		if !errors.As(err, &problem) || problem.Acceptance != "not-sent" || problem.SideEffects != "none" {
			t.Fatalf("invalid input %v", err)
		}
		if err := o.finish(); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 || diagnostic.Len() != 0 {
			t.Fatal("inspect rendered an error before its caller")
		}
	}
	unchanged, err := os.ReadFile(file)
	if err != nil || string(unchanged) != offlineWorkflow {
		t.Fatal("view modified workflow", err)
	}
}

func TestWorkflowDetailWrapPreservesUnicodeAndControls(t *testing.T) {
	input := "中👩🏽‍💻e\u0301\t\x1b[2J" + strings.Repeat("x", 200)
	lines := workflowWrap(input, 20)
	if strings.Join(lines, "") != inlineText(input) {
		t.Fatal("wrapped detail lost text")
	}
	for _, line := range lines {
		if cellWidth(line) > 20 || strings.ContainsRune(line, '\x1b') {
			t.Fatalf("unsafe line %q", line)
		}
	}
}

func TestWorkflowViewReadinessAndShortViewportFeedback(t *testing.T) {
	spec, plan := workflowFixture(t)
	for _, height := range []int{5, 6, 7, 8, 20} {
		v := workflowView{spec: spec, plan: plan, cacheStep: -1}
		frame := v.frame(60, height)
		text := terminalPickerText(frame)
		if !strings.Contains(text, "review [waiting]") || !strings.Contains(text, "not executed") {
			t.Fatalf("missing dependency state at height %d: %s", height, text)
		}
		if height >= 8 && !strings.Contains(text, "2 ready / 2 waiting") {
			t.Fatal("readiness counts missing", text)
		}
		v.message = "Filter is limited to 256 UTF-8 bytes."
		v.query.set("retained")
		frame = v.frame(60, height)
		if len(frame.Lines) > height-1 || !strings.Contains(terminalPickerText(frame), v.message) || !strings.Contains(terminalPickerText(frame), "Ctrl+U clear") || v.query.text != "retained" {
			t.Fatalf("short feedback lost at height %d: %+v", height, frame)
		}
	}
	if text := strings.Join(workflowStepText(spec.Steps[2], plan), "\n"); !strings.Contains(text, "dependency readiness: ready") || !strings.Contains(text, "Not executed") {
		t.Fatal("ready step implied execution", text)
	}
}
