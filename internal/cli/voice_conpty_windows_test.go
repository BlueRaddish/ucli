package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func TestPackagedConPTYVoiceDraftAndCancellation(t *testing.T) {
	requireVisibleQA(t)
	binary, fixturePath := os.Getenv("UCLI_CONPTY_BINARY"), os.Getenv("UCLI_TEST_VOICE_FIXTURE")
	if binary == "" || fixturePath == "" {
		t.Skip("opt in with packaged binary and tools/check_voice.py --prepare-only fake fixture; no real microphone")
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version  int    `json:"version"`
		Kind     string `json:"kind"`
		Python   string `json:"python"`
		Site     string `json:"site"`
		Model    string `json:"model"`
		Expected string `json:"expected_text"`
	}
	if json.Unmarshal(data, &fixture) != nil || fixture.Version != 1 || fixture.Kind != "ucli-fake-voice-no-hardware" || fixture.Expected != "/send remains literal" {
		t.Fatal("not the explicit synthetic voice fixture")
	}
	for _, path := range []string{fixture.Python, fixture.Site, fixture.Model} {
		if !filepath.IsAbs(path) {
			t.Fatal("fixture paths must be absolute")
		}
	}
	t.Setenv("UCLI_VOICE_PYTHON", fixture.Python)
	t.Setenv("UCLI_VOICE_SITE", fixture.Site)
	t.Setenv("UCLI_VOICE_MODEL_DIR", fixture.Model)
	t.Setenv("TERM", "xterm-256color")
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	original, err := a.Draft(context.Background(), "", "Already saved voice work", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false)
	p.awaitVisible(t, 0, "Already saved voice work")
	mark := p.send(t, "Pending composition 中")
	p.awaitVisible(t, mark, "Pending composition")
	open := func() {
		mark := p.send(t, "\t")
		p.awaitVisible(t, mark, "Actions")
		mark = p.send(t, "voice")
		p.awaitVisible(t, mark, "Search: voice")
		mark = p.send(t, "\r")
		p.awaitVisible(t, mark, "Recording · Synthetic microphone (no hardware)")
	}
	open()
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Pending composition")
	before, attempts, err := a.Task(context.Background(), original.ID)
	if err != nil || before.Draft != original.Draft || before.Revision != original.Revision || len(attempts) != 0 {
		t.Fatalf("canceled voice changed task: %+v %+v %v", before, attempts, err)
	}
	open()
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, fixture.Expected)
	before, attempts, err = a.Task(context.Background(), original.ID)
	if err != nil || before.Draft != original.Draft || before.Revision != original.Revision || len(attempts) != 0 {
		t.Fatalf("dictation saved or submitted before Enter: %+v %+v %v", before, attempts, err)
	}
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, fixture.Expected)
	mark = p.send(t, "/new\r")
	p.awaitVisible(t, mark, "New task selected")
	open()
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, fixture.Expected)
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Saved draft")
	p.send(t, "/exit\r")
	if code := p.wait(t); code != 0 {
		t.Fatalf("voice loop exit%d", code)
	}
	after, attempts, err := a.Task(context.Background(), original.ID)
	if err != nil || after.Draft != original.Draft+"\nPending composition 中\n"+fixture.Expected || after.Revision != original.Revision+1 || len(attempts) != 0 {
		t.Fatalf("composition/literal transcript changed: %+v %+v %v", after, attempts, err)
	}
	tasks, err := a.Tasks(context.Background())
	if err != nil || len(tasks) != 2 {
		t.Fatalf("voice task count: %+v %v", tasks, err)
	}
	for _, task := range tasks {
		_, attempts, err := a.Task(context.Background(), task.ID)
		if err != nil || len(attempts) != 0 {
			t.Fatalf("voice dispatched: %+v %v", attempts, err)
		}
		if task.ID != original.ID && task.Draft != fixture.Expected {
			t.Fatalf("leading slash transcript was treated as a command: %+v", task)
		}
	}
	for _, path := range []string{filepath.Join(home, "cache"), filepath.Join(home, "voice-env-v1")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("explicit fake-site voice attempted dependency/model setup")
		}
	}
}
