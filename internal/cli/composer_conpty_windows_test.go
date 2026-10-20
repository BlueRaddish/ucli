package cli

import (
	"context"
	"os"
	"testing"

	"ucli.local/ucli/internal/app"
)

func TestPackagedConPTYRichComposer(t *testing.T) {
	requireVisibleQA(t)
	t.Setenv("TERM", "xterm-256color")
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	home, cwd := t.TempDir(), t.TempDir()
	p := startConPTYOptions(t, binary, home, cwd, false)
	p.awaitVisible(t, 0, "Tab actions")
	mark := p.send(t, "/effort\r")
	p.awaitVisible(t, mark, "Choose effort")
	mark = p.send(t, "medium")
	p.awaitVisible(t, mark, "medium")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Effort changes reasoning intent")
	mark = p.send(t, "/models\r")
	p.awaitVisible(t, mark, "Choose a model")
	mark = p.send(t, "deep")
	p.awaitVisible(t, mark, "demo/deep")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Tab actions")
	p.resize(t, 60, 20)
	const pasted = "/exit\nLiteral paste 中 👩🏽‍💻 e\u0301"
	mark = p.send(t, "\x1b[200~"+pasted+"\x1b[201~\r")
	p.awaitVisible(t, mark, "Ready for your next prompt")
	mark = p.send(t, "/new\r")
	p.awaitVisible(t, mark, "New task selected")
	mark = p.send(t, "Partial 中 e\u0301")
	p.awaitVisible(t, mark, "Partial")
	p.send(t, "\x03\x03")
	if code := p.wait(t); code != 130 {
		t.Fatalf("partial interrupt exit=%d", code)
	}

	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tasks, err := a.Tasks(context.Background())
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	for _, task := range tasks {
		_, attempts, err := a.Task(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Draft == "Partial 中 e\u0301" {
			if len(attempts) != 0 {
				t.Fatal("interrupt submitted partial input")
			}
		} else if len(attempts) != 1 || attempts[0].Input != pasted || attempts[0].Requested.Model != "auto" || attempts[0].Requested.Effort != "medium" {
			t.Fatalf("paste/selection/cancel mismatch: task=%+v attempts=%+v", task, attempts)
		}
	}
	p = startConPTYOptions(t, binary, home, cwd, false)
	p.awaitVisible(t, 0, "Restored your unsent message")
	p.awaitVisible(t, 0, "Partial")
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("raw EOF exit=%d", code)
	}
}
