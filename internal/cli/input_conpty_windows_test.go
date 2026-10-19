package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
)

func TestPackagedConPTYRejectLossyPaste(t *testing.T) {
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows executable")
	}
	t.Setenv("TERM", "xterm-256color")
	for _, test := range []struct{ name, input, diagnostic string }{
		{"nul", "prefix\x00suffix", "without NUL"},
		{"replacement_character", "prefix\ufffdsuffix", "cannot preserve U+FFFD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			p := startConPTYOptions(t, binary, home, cwd, false)
			p.await(t, 0, "Tab actions")
			mark := p.send(t, "\x1b[200~"+test.input+"\x1b[201~")
			p.await(t, mark, test.diagnostic)
			if code := p.wait(t); code != 2 {
				t.Fatalf("invalid paste exit%d, want2", code)
			}
			a, err := app.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			tasks, err := a.Tasks(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range tasks {
				_, attempts, err := a.Task(context.Background(), task.ID)
				if err != nil || len(attempts) != 0 || strings.Contains(task.Draft, "prefix") {
					t.Fatalf("invalid paste was saved or dispatched: %+v %+v %v", task, attempts, err)
				}
			}
		})
	}
}

// Diagnostic only: inspect the actual synthetic console records before applying
// UCLI's transport. No application, credentials or microphone is involved.
func TestConPTYInputRecordObservation(t *testing.T) {
	binary := os.Getenv("UCLI_CONPTY_RECORD_PROBE")
	if binary == "" {
		t.Skip("explicit synthetic console record probe opt-in")
	}
	p := startConPTYOptions(t, binary, t.TempDir(), t.TempDir(), false)
	p.await(t, 0, "Synthetic record probe ready")
	mark := p.send(t, "\x1b[200~prefix\x00suffix\x1b[201~!")
	p.await(t, mark, "Synthetic record probe finished")
	if code := p.wait(t); code != 0 {
		t.Fatalf("record probe exit%d", code)
	}
}
