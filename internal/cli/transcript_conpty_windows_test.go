package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
)

// This consumes the packaged rich terminal and durable demo receipt, not the
// design mockup. Stream-growth anchors are additionally exercised by the real
// typed-message reducer tests without artificial production delays.
func TestPackagedConPTYTranscriptNavigation(t *testing.T) {
	requireVisibleQA(t)
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	t.Setenv("TERM", "xterm-256color")
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-color", "--no-motion")
	p.awaitVisible(t, 0, "Enter send")
	prompt := "LATEST TASK 日本語\n"
	for i := range 24 {
		prompt += fmt.Sprintf("Source line %02d keeps its place.\n", i)
	}
	prompt += "End of the submitted prompt."
	mark := p.send(t, "\x1b[200~"+prompt+"\x1b[201~\r")
	p.awaitVisible(t, mark, "Ready for your next prompt")
	awaitShellFrame(t, p, p.resizes, "transcript-following", func(f shellCapturedFrame) bool {
		return shellFramePrompt(f, 80, 24, "Ready", ">") &&
			strings.Contains(strings.Join(f.Rows, "\n"), "Latest prompt") &&
			strings.Contains(strings.Join(f.Rows, "\n"), "Assistant")
	})
	tasks, err := a.Tasks(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("submission did not create exactly one task: %+v %v", tasks, err)
	}
	_, attempts, err := a.Task(context.Background(), tasks[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Input != prompt || attempts[0].State != "complete" {
		t.Fatalf("display/navigation fixture was not durably accepted: %+v %v", attempts, err)
	}
	const draft = "Keep this unsent"
	p.send(t, draft)
	p.send(t, "\x1b[1;5A") // Ctrl+Up: latest prompt, not the first retained prompt.
	awaitShellFrame(t, p, p.resizes, "transcript-latest-prompt", func(f shellCapturedFrame) bool {
		return shellFramePrompt(f, 80, 24, "Ready", "> "+draft) &&
			strings.Contains(f.Rows[3], "| Your prompt") &&
			strings.Contains(f.Rows[4], "| LATEST TASK 日本語") &&
			strings.Contains(strings.Join(f.Rows, "\n"), "Following paused")
	})
	p.resize(t, 60, 20)
	awaitShellFrame(t, p, p.resizes, "transcript-anchor-resized", func(f shellCapturedFrame) bool {
		return shellFramePrompt(f, 60, 20, "Ready", "> "+draft) &&
			strings.Contains(f.Rows[3], "| Your prompt") &&
			strings.Contains(f.Rows[4], "| LATEST TASK 日本語")
	})
	p.send(t, "\x1b[1;5F") // Ctrl+End independently resumes the live output tail.
	awaitShellFrame(t, p, p.resizes, "transcript-latest-output", func(f shellCapturedFrame) bool {
		text := strings.Join(f.Rows, "\n")
		return shellFramePrompt(f, 60, 20, "Ready", "> "+draft) &&
			strings.Contains(text, "simulated result") && !strings.Contains(text, "Following paused")
	})
	mark = p.send(t, "\t")
	p.awaitVisible(t, mark, "Actions")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, draft)
	for _, sequence := range []string{"\x1b[?1000h", "\x1b[?1002h", "\x1b[?1003h"} {
		if strings.Contains(p.snapshot(), sequence) {
			t.Fatal("transcript enabled mouse capture and compromised native selection")
		}
	}
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("EOF exit=%d", code)
	}
	stored, attempts, err := a.Task(context.Background(), tasks[0].ID)
	if err != nil || stored.Draft != draft || len(attempts) != 1 {
		t.Fatalf("navigation or EOF changed/resent the draft: %+v %+v %v", stored, attempts, err)
	}
}

func TestPackagedConPTYTranscriptBands(t *testing.T) {
	requireVisibleQA(t)
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	for _, theme := range []struct{ name, response, background string }{
		{"dark", "rgb:0000/0000/0000", "48;2;35;47;61"},
		{"light", "rgb:ffff/ffff/ffff", "48;2;229;237;245"},
	} {
		t.Run(theme.name, func(t *testing.T) {
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("COLORTERM", "truecolor")
			t.Setenv("NO_COLOR", "")
			p := startConPTYOptions(t, binary, t.TempDir(), t.TempDir(), false, "--no-motion")
			p.awaitVisible(t, 0, "Enter send")
			// Reply only to this owned virtual terminal's background query. No
			// desktop terminal or theme settings are inspected or modified.
			p.send(t, "\x1b]11;"+theme.response+"\x1b\\")
			mark := p.send(t, "Explain this local demonstration\r")
			p.awaitVisible(t, mark, "Ready for your next prompt")
			awaitShellFrame(t, p, p.resizes, "transcript-band-"+theme.name, func(f shellCapturedFrame) bool {
				text := strings.Join(f.Rows, "\n")
				return shellFramePrompt(f, 80, 24, "Ready", ">") &&
					strings.Contains(text, "| Your prompt") && strings.Contains(text, "| Explain this local") &&
					strings.Contains(text, "Assistant") && len(f.Colors) > 0
			})
			if !strings.Contains(p.snapshot(), theme.background) {
				t.Fatalf("%s prompt never emitted its paired background", theme.name)
			}
			p.send(t, "\x04")
			if code := p.wait(t); code != 0 {
				t.Fatalf("EOF exit=%d", code)
			}
		})
	}
}
