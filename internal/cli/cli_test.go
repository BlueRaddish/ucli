package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func call(t *testing.T, home string, input string, args ...string) (int, string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := Run(context.Background(), append([]string{"--home", home}, args...), strings.NewReader(input), &out, &diagnostic)
	return code, out.String(), diagnostic.String()
}

type envelope struct {
	Version int             `json:"version"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}

func jsonLines(t *testing.T, text string) []envelope {
	t.Helper()
	var result []envelope
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		var item envelope
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("not pure JSON lines: %q: %v", line, err)
		}
		if item.Version != 1 || item.Type == "" {
			t.Fatalf("unversioned output: %s", line)
		}
		result = append(result, item)
	}
	return result
}

func taskReceipt(t *testing.T, home, id string) (core.Task, []core.Attempt) {
	t.Helper()
	code, out, diagnostic := call(t, home, "", "tasks", "show", id, "--json")
	if code != 0 {
		t.Fatalf("show: %d %s %s", code, out, diagnostic)
	}
	items := jsonLines(t, out)
	var data struct {
		Task     core.Task      `json:"task"`
		Attempts []core.Attempt `json:"attempts"`
	}
	if err := json.Unmarshal(items[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	return data.Task, data.Attempts
}

func attemptReceipt(t *testing.T, text string) core.Attempt {
	t.Helper()
	for _, item := range jsonLines(t, text) {
		if item.Type == "attempt" {
			var a core.Attempt
			if err := json.Unmarshal(item.Data, &a); err != nil {
				t.Fatal(err)
			}
			return a
		}
	}
	t.Fatal("no durable attempt receipt")
	return core.Attempt{}
}

func TestHelpAndVersionAvoidState(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	for _, args := range [][]string{{"--help"}, {"--version"}, {"--bad", "run", "--help"}, {"help", "tasks", "show"}, {"--no-input"}, {"--json", "--help"}} {
		code, out, diagnostic := call(t, missing, "", args...)
		if code != 0 || out == "" || diagnostic != "" {
			t.Fatalf("%v: %d %s %s", args, code, out, diagnostic)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("help opened state: %v", err)
	}
	for _, item := range commands {
		code, out, diagnostic := call(t, missing, "", append(strings.Fields(item.name), "--help")...)
		if code != 0 || diagnostic != "" || !strings.Contains(out, "EXAMPLES\n") {
			t.Fatalf("help %s: %d %s", item.name, code, diagnostic)
		}
		for _, line := range strings.Split(out, "\n") {
			if len(line) > 95 {
				t.Fatalf("overlong help (%s): %s", item.name, line)
			}
		}
	}
}

func TestParserFlagsLiteralInputAndNoSilentDefaults(t *testing.T) {
	p, err := parse([]string{"--model", "demo/deep", "run", "first", "--effort=high", "second", "--", "--account", "literal", "--help"})
	if err != nil || p.help || p.prompt != "first second --account literal --help" || p.selection.Model != "demo/deep" || p.selection.Effort != "high" {
		t.Fatalf("%+v %v", p, err)
	}
	if p.selection.Account != "" || p.selection.Permission != "" {
		t.Fatal("absent flags would overwrite followup pins")
	}
	for _, args := range [][]string{{"rn"}, {"run"}, {"models", "--model", "auto"}, {"run", "a", "--wat"}, {"run", "a", "--demo=true"}, {"tasks", "followup", "id"}, {"--home="}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted invalid argv %v", args)
		}
	}
}

func TestScriptedTaskLoopAndIdempotency(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	args := []string{"run", "--demo", "--json", "--cwd", cwd, "--model", "demo/deep", "--effort", "medium", "--command-id", "cli-first", "First task"}
	code, out, diagnostic := call(t, home, "", args...)
	if code != 0 || diagnostic != "" {
		t.Fatalf("run %d %s %s", code, out, diagnostic)
	}
	first := attemptReceipt(t, out)
	if first.State != "complete" || first.TaskID == "" || first.Effective.Model != "demo/deep" {
		t.Fatalf("%+v", first)
	}
	code, out, _ = call(t, home, "", args...)
	if code != 0 || attemptReceipt(t, out).ID != first.ID {
		t.Fatal("repeat command did not return the same attempt")
	}
	code, out, diagnostic = call(t, home, "", "tasks", "followup", first.TaskID, "--demo", "--json", "Explain the result")
	if code != 0 {
		t.Fatalf("followup %d %s %s", code, out, diagnostic)
	}
	second := attemptReceipt(t, out)
	if second.TaskID != first.TaskID || second.ID == first.ID || second.Revision <= first.Revision || second.Requested.Model != "demo/deep" || second.Requested.Effort != "medium" {
		t.Fatalf("followup lost thread/pins: %+v", second)
	}
	_, attempts := taskReceipt(t, home, first.TaskID)
	if len(attempts) != 2 || attempts[0].Input != "First task" || attempts[1].Input != "Explain the result" {
		t.Fatalf("history changed: %+v", attempts)
	}
	code, out, _ = call(t, home, "", "run", "--demo", "--json", "--cwd", cwd, "Fresh work")
	if code != 0 || attemptReceipt(t, out).TaskID == first.TaskID {
		t.Fatal("standalone run reused old task")
	}
}

func TestPrerequisiteAndUnsupportedEffortPreserveDraft(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	code, out, diagnostic := call(t, home, "", "run", "--json", "--cwd", cwd, "Remember this work")
	if code != 3 || diagnostic != "" {
		t.Fatalf("%d %s %s", code, out, diagnostic)
	}
	items := jsonLines(t, out)
	var problem core.Error
	if err := json.Unmarshal(items[len(items)-1].Data, &problem); err != nil {
		t.Fatal(err)
	}
	task, attempts := taskReceipt(t, home, problem.TaskID)
	if task.Draft != "Remember this work" || len(attempts) != 0 {
		t.Fatalf("lost unsent draft: %+v %+v", task, attempts)
	}
	code, out, _ = call(t, home, "", "tasks", "followup", task.ID, "--demo", "--json", "--model", "demo/fast", "--effort", "high", "Remember this work")
	if code != 5 {
		t.Fatalf("unsupported effort %d %s", code, out)
	}
	task, attempts = taskReceipt(t, home, task.ID)
	if task.Selection.Effort != "high" || task.Draft != "Remember this work" || len(attempts) != 0 {
		t.Fatal("failed settings/input not preserved")
	}
	code, out, _ = call(t, home, "", "tasks", "followup", task.ID, "--demo", "--json", "--effort", "low", "Remember this work")
	if code != 0 || attemptReceipt(t, out).TaskID != task.ID {
		t.Fatalf("recovery %d %s", code, out)
	}
}

func TestRedirectedInputAndMachineOutput(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	code, out, diagnostic := call(t, home, "line one\nline two\n", "run", "--demo", "--json", "--cwd", cwd, "-")
	if code != 0 || diagnostic != "" || attemptReceipt(t, out).Input != "line one\nline two\n" {
		t.Fatalf("%d %s %s", code, out, diagnostic)
	}
	for _, args := range [][]string{{"models"}, {"accounts"}, {"tasks"}, {"doctor"}, {"explain", "--demo"}} {
		code, out, diagnostic = call(t, home, "", append(args, "--json")...)
		if code != 0 || diagnostic != "" {
			t.Fatalf("%v: %d %s %s", args, code, out, diagnostic)
		}
		jsonLines(t, out)
		if args[0] == "doctor" {
			var doctor struct {
				Managed string `json:"managed_providers"`
				Echo    string `json:"local_echo"`
			}
			if err := json.Unmarshal(jsonLines(t, out)[0].Data, &doctor); err != nil || doctor.Managed != "unadmitted" || doctor.Echo == "" {
				t.Fatalf("doctor conflated local echo with managed AI: %s %v", out, err)
			}
		}
	}
	code, out, diagnostic = call(t, home, "do not send this", "--no-input")
	if code != 0 || !strings.Contains(out, "Usage:") || diagnostic != "" {
		t.Fatal("bare redirected invocation should show brief help")
	}
	code, _, _ = call(t, home, strings.Repeat("x", (1<<20)+1), "run", "--demo", "-")
	if code != 2 {
		t.Fatalf("oversized input: %d", code)
	}
}

func runInteractive(t *testing.T, home, cwd, input string, demo bool) (int, string) {
	t.Helper()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var text bytes.Buffer
	o := newOutput(&text, &text, false, cancel)
	code := interact(ctx, a, invocation{cwd: cwd, demo: demo}, strings.NewReader(input), o)
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	return code, text.String()
}

func savedTasks(t *testing.T, home string) []core.Task {
	t.Helper()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tasks, err := a.Tasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestInteractiveChoicesFollowupNewAndHistory(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	input := "/models\nLocal demo\n1\ndeep\n1\n1\n/effort\nmedium\n1\n/accounts\nlocal\n1\nFirst prompt\n\nFollow up\n/result\n/new\n/draft New draft\n/exit\n"
	code, text := runInteractive(t, home, cwd, input, true)
	if code != 0 {
		t.Fatalf("%d %s", code, text)
	}
	for _, want := range []string{"Model selected", "First prompt", "Follow up", "New task selected", "DEMO enabled"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	tasks := savedTasks(t, home)
	if len(tasks) != 2 {
		t.Fatalf("tasks: %+v", tasks)
	}
	for _, task := range tasks {
		_, attempts := taskReceipt(t, home, task.ID)
		if task.Draft == "New draft" {
			if len(attempts) != 0 {
				t.Fatal("new draft ran")
			}
			continue
		}
		if len(attempts) != 2 || attempts[0].Input != "First prompt" || attempts[1].Input != "Follow up" || attempts[1].Requested.Model != "demo/deep" {
			t.Fatalf("unexpected thread: %+v", attempts)
		}
	}
}

func TestInteractiveFailureAndEOFDraftRestore(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	code, text := runInteractive(t, home, cwd, "Unsent work\n/exit\n", false)
	if code != 0 || !strings.Contains(text, "Connect a native account") {
		t.Fatalf("%d %s", code, text)
	}
	tasks := savedTasks(t, home)
	if len(tasks) != 1 || tasks[0].Draft != "Unsent work" {
		t.Fatalf("drafts: %+v", tasks)
	}
	code, text = runInteractive(t, home, cwd, "/demo\n/send\n/exit\n", false)
	if code != 0 || !strings.Contains(text, "Restored your unsent message") {
		t.Fatalf("%d %s", code, text)
	}
	_, attempts := taskReceipt(t, home, tasks[0].ID)
	if len(attempts) != 1 {
		t.Fatal("did not recover same draft")
	}
	code, text = runInteractive(t, home, cwd, "/new\nPartial line /send", true)
	if code != 0 {
		t.Fatalf("%d %s", code, text)
	}
	for _, task := range savedTasks(t, home) {
		if task.Draft == "Partial line /send" {
			_, a := taskReceipt(t, home, task.ID)
			if len(a) != 0 {
				t.Fatal("EOF autosubmitted")
			}
			return
		}
	}
	t.Fatal("EOF did not preserve partial text literally")
}

type triggerWriter struct {
	bytes.Buffer
	once    sync.Once
	trigger string
	action  func()
}

func (w *triggerWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if strings.Contains(w.Buffer.String(), w.trigger) {
		w.once.Do(w.action)
	}
	return n, err
}
func (w *triggerWriter) WriteString(text string) (int, error) { return w.Write([]byte(text)) }

func TestInteractiveContextInterruptKeepsAcknowledgedDraft(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	if _, err := a.Draft(ctx, "", "Keep this draft", cwd, core.Defaults()); err != nil {
		t.Fatal(err)
	}
	// Interrupt while reviewing already saved work, before any send gesture.
	out := &triggerWriter{trigger: "Keep this draft", action: cancel}
	o := newOutput(out, out, false, cancel)
	code := interact(ctx, a, invocation{cwd: cwd, demo: true}, reader, o)
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if code != 130 {
		t.Fatalf("interrupt exit %d: %s", code, out.String())
	}
	tasks, err := a.Tasks(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Draft != "Keep this draft" {
		t.Fatalf("%+v %v", tasks, err)
	}
}

type blockedWriter struct{ gate chan struct{} }

func (w blockedWriter) Write(p []byte) (int, error) { <-w.gate; return len(p), nil }

func TestBlockedOutputReturnsAndTaskIsDurable(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	gate := make(chan struct{})
	defer close(gate)
	start := time.Now()
	code := Run(context.Background(), []string{"--home", home, "run", "--demo", "--cwd", cwd, "Output may block"}, strings.NewReader(""), blockedWriter{gate}, io.Discard)
	if code != 9 || time.Since(start) > 3*time.Second {
		t.Fatalf("blocked reader did not return promptly: %d %v", code, time.Since(start))
	}
	tasks := savedTasks(t, home)
	if len(tasks) != 1 {
		t.Fatalf("missing task: %+v", tasks)
	}
	_, attempts := taskReceipt(t, home, tasks[0].ID)
	if len(attempts) != 1 || attempts[0].State != "complete" {
		t.Fatalf("durability lost: %+v", attempts)
	}
}

func TestRendererEscapesTerminalControls(t *testing.T) {
	unsafe := "Hello\x1b[2J\x1b]52;c;clipboard\a\r\u202e world\n\tUTF-8: 한글"
	text := safe(unsafe)
	if strings.ContainsAny(text, "\x1b\a\r\u202e") || !strings.Contains(text, "한글") || !strings.Contains(text, "\\u001b") {
		t.Fatalf("unsafe output: %q", text)
	}
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	o := newOutput(&out, io.Discard, true, cancel)
	if err := o.event(core.Event{Version: 1, Type: "message_delta", Text: unsafe}); err != nil {
		t.Fatal(err)
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	jsonLines(t, out.String())
	if strings.ContainsRune(out.String(), '\x1b') {
		t.Fatal("raw escape in JSON")
	}
}

func TestRedirectedPromptCancellationNeedsNoState(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := 130
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = 124
		} else {
			cancel()
		}
		reader, writer := io.Pipe()
		home := filepath.Join(t.TempDir(), "not-created")
		var out, diagnostic bytes.Buffer
		code := Run(ctx, []string{"--home", home, "run", "--json", "-"}, reader, &out, &diagnostic)
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		if code != want || diagnostic.Len() != 0 {
			t.Fatalf("wanted %d: %d %s %s", want, code, out.String(), diagnostic.String())
		}
		jsonLines(t, out.String())
		if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled read opened store: %v", err)
		}
	}
}

func TestInteractiveConcurrentDraftConflictRequiresExplicitSelection(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	other, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan error, 1)
	var originalID string
	if _, err := a.Draft(ctx, "", "Original draft", cwd, core.Defaults()); err != nil {
		t.Fatal(err)
	}
	out := &triggerWriter{trigger: "Original draft"}
	out.action = func() {
		defer writer.Close()
		tasks, err := other.Tasks(ctx)
		if err != nil {
			done <- err
			return
		}
		if len(tasks) != 1 {
			done <- errors.New("expected one saved draft before concurrent change")
			return
		}
		originalID = tasks[0].ID
		_, err = other.Draft(ctx, originalID, "Newer task text", cwd, core.Defaults())
		if err != nil {
			done <- err
			return
		}
		_, err = io.WriteString(writer, "/result\n/older\n/tasks\n0\nStale addition\n/send\n/draft replacement\n/result\n/tasks\nRecovered draft\n1\n/send\n/exit\n")
		done <- err
	}
	o := newOutput(out, out, false, cancel)
	code := interact(ctx, a, invocation{cwd: cwd, demo: true}, reader, o)
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("%d %s", code, out.String())
	}
	for _, want := range []string{"Draft conflict", "Unsent input (saved separately)", "Editing and Send are blocked", "Copy these additional edits", "Opened task"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	original, attempts, err := a.Task(ctx, originalID)
	if err != nil || original.Draft != "Newer task text" || len(attempts) != 0 {
		t.Fatalf("overwrote newer task: %+v %+v %v", original, attempts, err)
	}
	tasks, err := a.Tasks(ctx)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("%+v %v", tasks, err)
	}
	for _, task := range tasks {
		if task.ID == originalID {
			continue
		}
		_, attempts, err := a.Task(ctx, task.ID)
		if err != nil || len(attempts) != 1 || attempts[0].Input != "Original draft\nStale addition" {
			t.Fatalf("recovery draft not preserved/submitted once: %+v %v", attempts, err)
		}
	}
}

func historyFixture(t *testing.T, home, cwd string) []core.Attempt {
	t.Helper()
	var attempts []core.Attempt
	for i := 1; i <= 7; i++ {
		args := []string{"run", "--cwd", cwd}
		if len(attempts) != 0 {
			args = []string{"tasks", "followup", attempts[0].TaskID}
		}
		args = append(args, "--demo", "--json", fmt.Sprintf("History input %d", i))
		code, out, diagnostic := call(t, home, "", args...)
		if code != 0 || diagnostic != "" {
			t.Fatalf("history setup: %d %s %s", code, out, diagnostic)
		}
		attempts = append(attempts, attemptReceipt(t, out))
	}
	return attempts
}

func TestTaskHistoryPaginationAndFlagValidation(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	attempts := historyFixture(t, home, cwd)
	id := attempts[0].TaskID
	code, out, diagnostic := call(t, home, "", "tasks", "show", id)
	if code != 0 || diagnostic != "" || strings.Contains(out, "Attempt "+attempts[1].ID) || !strings.Contains(out, "Attempt "+attempts[2].ID) || !strings.Contains(out, "--before-revision") {
		t.Fatalf("default recent page: %d %s %s", code, out, diagnostic)
	}
	before := 0
	seen := map[string]bool{}
	for _, wantCount := range []int{3, 3, 1} {
		code, out, diagnostic = call(t, home, "", "--limit", "3", "tasks", "show", id, "--before-revision", fmt.Sprint(before), "--json")
		if code != 0 || diagnostic != "" {
			t.Fatalf("page: %d %s %s", code, out, diagnostic)
		}
		items := jsonLines(t, out)
		var page struct {
			Task       core.Task         `json:"task"`
			Attempts   []core.Attempt    `json:"attempts"`
			Pagination historyPagination `json:"pagination"`
		}
		if err := json.Unmarshal(items[0].Data, &page); err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].Type != "task" || page.Task.ID != id || len(page.Attempts) != wantCount || page.Pagination.Limit != 3 || page.Pagination.BeforeRevision != before {
			t.Fatalf("invalid page: %+v", page)
		}
		lastRevision := 0
		for _, attempt := range page.Attempts {
			if seen[attempt.ID] || attempt.Revision <= lastRevision || (before != 0 && attempt.Revision >= before) {
				t.Fatalf("duplicate/out-of-order attempt: %+v", attempt)
			}
			seen[attempt.ID] = true
			lastRevision = attempt.Revision
		}
		before = page.Pagination.NextBeforeRevision
		if wantCount > 1 && before != page.Attempts[0].Revision {
			t.Fatalf("cursor did not continue from oldest displayed revision: %+v", page)
		}
	}
	if before != 0 || len(seen) != len(attempts) {
		t.Fatalf("history incomplete: cursor=%d seen=%d", before, len(seen))
	}
	code, out, _ = call(t, home, "", "tasks", "show", id, "--before-revision", "1")
	if code != 0 || !strings.Contains(out, "No attempts before revision 1") || strings.Contains(out, "No attempt has run") {
		t.Fatalf("empty historical page misrepresented task: %d %s", code, out)
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	for _, args := range [][]string{{"tasks", "show", id, "--limit", "0"}, {"tasks", "show", id, "--limit", "101"}, {"tasks", "show", id, "--limit", "1.5"}, {"tasks", "show", id, "--before-revision", "-1"}, {"tasks", "show", id, "--before-revision", "999999999999999999999"}, {"models", "--limit", "5"}, {"--before-revision", "0"}} {
		if code, _, _ = call(t, missing, "", args...); code != 2 {
			t.Fatalf("invalid history args %v exit=%d", args, code)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid pagination opened state: %v", err)
	}
}

func TestInteractiveHistoryPagingPreservesTaskAndDraft(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	attempts := historyFixture(t, home, cwd)
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := a.Draft(context.Background(), attempts[0].TaskID, "Keep this unsent draft", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	code, text := runInteractive(t, home, cwd, "/result\n/older\n/older\n/result\n/new\n/older\n/exit\n", true)
	if code != 0 {
		t.Fatalf("history navigation: %d %s", code, text)
	}
	for i, attempt := range attempts {
		want := 2
		if i < 2 {
			want = 1
		}
		if count := strings.Count(text, "Attempt "+attempt.ID+" |"); count != want {
			t.Fatalf("attempt%d shown %d times, want%d: %s", i, count, want, text)
		}
	}
	for _, want := range []string{"Older: /older", "No older attempts", "Use /result to open recent results first"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing navigation feedback %q: %s", want, text)
		}
	}
	tasks := savedTasks(t, home)
	if len(tasks) != 1 || tasks[0].ID != draft.ID || tasks[0].Revision != draft.Revision || tasks[0].Draft != draft.Draft || tasks[0].Selection != draft.Selection {
		t.Fatalf("history navigation changed task/draft: %+v", tasks)
	}
}

type heldWriter struct {
	entered, release chan struct{}
	once             sync.Once
}

func (w *heldWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestRendererFinishWithInflightFullQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &heldWriter{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	o := newOutput(w, io.Discard, false, cancel)
	if err := o.text("first write"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("writer never started")
	}
	for range cap(o.queue) {
		if err := o.text("queued output"); err != nil {
			t.Fatal(err)
		}
	}
	callbackDone := make(chan error, 1)
	go func() {
		callbackDone <- o.event(core.Event{Version: 1, Type: "message_delta", Text: "in-flight callback"})
	}()
	start := time.Now()
	if err := o.finish(); err == nil {
		t.Fatal("blocked output finish claimed success")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("finish exceeded its bound")
	}
	select {
	case err := <-callbackDone:
		if err == nil {
			t.Fatal("late callback accepted after finish")
		}
	case <-time.After(time.Second):
		t.Fatal("callback remained blocked")
	}
	if ctx.Err() == nil {
		t.Fatal("output timeout did not cancel service")
	}
	if err := o.event(core.Event{Version: 1, Type: "message_delta", Text: "after finish"}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("late write: %v", err)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRendererClosedWriterAndLateCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := newOutput(brokenWriter{}, io.Discard, false, cancel)
	_ = o.text("result")
	if err := o.finish(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error lost: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("closed writer did not cancel service")
	}
	if err := o.event(core.Event{Version: 1, Type: "message_delta", Text: "late"}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("late callback: %v", err)
	}
}

func TestRendererFlushBeforeSuccessfulReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	var out bytes.Buffer
	o := newOutput(&out, io.Discard, false, cancel)
	for _, text := range []string{"first\n", "second\n", "last\n"} {
		if err := o.text(text); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "first\nsecond\nlast\n" {
		t.Fatalf("unflushed output: %q", out.String())
	}
	if err := o.text("too late"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("late output: %v", err)
	}
}
