package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workflow"
)

const offlineWorkflow = `{"version":1,"id":"offline-example","steps":[
{"id":"review","prompt":"SECRET review text","depends_on":["build"]},
{"id":"build","prompt":"SECRET implement text","depends_on":["inspect"]},
{"id":"lint","prompt":"SECRET lint text"},
{"id":"inspect","prompt":"SECRET inspect text"}]}`

func TestWorkflowFileAndStdinCheckWithoutStateOrDispatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(file, []byte(offlineWorkflow), 0600); err != nil {
		t.Fatal(err)
	}
	brokenHome := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(brokenHome, []byte("sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	code, text, diagnostic := call(t, brokenHome, "", "workflow", "check", file)
	if code != 0 || diagnostic != "" || !strings.Contains(text, "No tasks were accepted or started") || !strings.Contains(text, "Ready (2)") || !strings.Contains(text, "Blocked (2)") || strings.Contains(text, "SECRET") {
		t.Fatalf("offline file check: %d %s %s", code, text, diagnostic)
	}
	code, text, diagnostic = call(t, brokenHome, offlineWorkflow, "--json", "workflow", "check", "-")
	if code != 0 || diagnostic != "" {
		t.Fatalf("offline stdin check: %d %s %s", code, text, diagnostic)
	}
	rows := jsonLines(t, text)
	var result struct {
		SpecID     string        `json:"spec_id"`
		Offline    bool          `json:"offline"`
		Dispatched bool          `json:"dispatched"`
		Plan       workflow.Plan `json:"plan"`
	}
	if len(rows) != 1 || rows[0].Type != "workflow_plan" {
		t.Fatalf("check emitted execution events: %+v", rows)
	}
	if err := json.Unmarshal(rows[0].Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.SpecID != "offline-example" || !result.Offline || result.Dispatched || strings.Join(result.Plan.Ready, ",") != "inspect,lint" || len(result.Plan.Blocked) != 2 || len(result.Plan.Running) != 0 || len(result.Plan.Complete) != 0 {
		t.Fatalf("unexpected initial plan: %+v", result)
	}
	for _, blocked := range result.Plan.Blocked {
		if blocked.State != workflow.Pending || len(blocked.Reasons) != 1 || blocked.Reasons[0].Dependency == "" {
			t.Fatalf("blocked reason missing: %+v", blocked)
		}
	}
	if strings.Contains(text, "SECRET") {
		t.Fatal("offline summary exposed prompts")
	}
	unchanged, err := os.ReadFile(file)
	if err != nil || string(unchanged) != offlineWorkflow {
		t.Fatal("check modified the input file", err)
	}
	unchanged, err = os.ReadFile(brokenHome)
	if err != nil || string(unchanged) != "sentinel" {
		t.Fatal("check touched the ignored state home", err)
	}
}

func TestWorkflowInvalidInputAndDiscoveryAvoidState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	for _, args := range [][]string{{"workflow"}, {"workflow", "--help"}, {"help", "workflow", "check"}} {
		code, text, diagnostic := call(t, home, "", args...)
		if code != 0 || diagnostic != "" || !strings.Contains(text, "OFFLINE CHECK") || !strings.Contains(text, "workflow check") {
			t.Fatalf("workflow discovery %v: %d %s %s", args, code, text, diagnostic)
		}
	}
	for _, input := range []string{
		`{`,
		`{"version":2,"id":"bad","steps":[]}`,
		`{"version":1,"id":"bad","steps":[{"id":"a","prompt":"hidden","depends_on":["b"]},{"id":"b","prompt":"hidden","depends_on":["a"]}]}`,
		strings.Repeat(" ", (2<<20)+1),
	} {
		code, text, diagnostic := call(t, home, input, "workflow", "check", "-", "--json")
		if code != 2 || diagnostic != "" {
			t.Fatalf("invalid workflow exit=%d stdout=%s stderr=%s", code, text, diagnostic)
		}
		rows := jsonLines(t, text)
		var problem core.Error
		if len(rows) != 1 || rows[0].Type != "error" || json.Unmarshal(rows[0].Data, &problem) != nil || !strings.HasPrefix(problem.Code, "workflow_") {
			t.Fatalf("untyped workflow error: %s", text)
		}
	}
	for _, args := range [][]string{{"workflow", "check"}, {"workflow", "run", "no-file"}, {"workflow", "check", "-", "--demo"}, {"workflow", "check", "-", "--model", "auto"}, {"workflow", "check", "-", "--limit", "1"}, {"workflow", "check", filepath.Join(t.TempDir(), "missing")}, {"workflow", "check", t.TempDir()}} {
		code, text, diagnostic := call(t, home, offlineWorkflow, append(args, "--json")...)
		if code != 2 || diagnostic != "" || jsonLines(t, text)[0].Type != "error" {
			t.Fatalf("invalid args %v: %d %s %s", args, code, text, diagnostic)
		}
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("offline route opened state: %v", err)
	}
}

type observedReader struct {
	io.Reader
	entered chan struct{}
	once    sync.Once
}

func (r *observedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	return r.Reader.Read(p)
}

func TestWorkflowInterruptedStdinReturnsWithoutOpeningState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	observed := &observedReader{Reader: reader, entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var text, diagnostic bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"--home", home, "workflow", "check", "-", "--json"}, observed, &text, &diagnostic)
	}()
	select {
	case <-observed.entered:
	case <-time.After(time.Second):
		t.Fatal("workflow did not begin reading redirected input")
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 || diagnostic.Len() != 0 || jsonLines(t, text.String())[0].Type != "error" || !strings.Contains(text.String(), "No tasks were accepted") || strings.Contains(text.String(), "inspect ucli tasks") {
			t.Fatalf("interrupted workflow: %d %s %s", code, text.String(), diagnostic.String())
		}
	case <-time.After(time.Second):
		t.Fatal("workflow checker stayed blocked on caller input")
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted check opened state: %v", err)
	}
}
