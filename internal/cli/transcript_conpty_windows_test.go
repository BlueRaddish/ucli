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

// On the verified Windows ConPTY route OSC 11 did not establish a known
// background. Assert the actual palette-preserving fallback, not an assumed RGB
// response. Exact known-light/dark pairs are covered through BackgroundColorMsg.
func TestPackagedConPTYTranscriptBands(t *testing.T) {
	requireVisibleQA(t)
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("NO_COLOR", "")
	p := startConPTYOptions(t, binary, t.TempDir(), t.TempDir(), false, "--no-motion")
	p.awaitVisible(t, 0, "Enter send")
	mark := p.send(t, "Explain this local demonstration\r")
	p.awaitVisible(t, mark, "Ready for your next prompt")
	awaitShellFrame(t, p, p.resizes, "transcript-band-default-palette", transcriptDefaultBandMatches)
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("EOF exit=%d", code)
	}
}

func transcriptDefaultBandMatches(frame shellCapturedFrame) bool {
	if !shellFramePrompt(frame, 80, 24, "Ready", ">") {
		return false
	}
	prompt, caption, reply, answer := false, false, false, false
	for row, text := range frame.Rows {
		expect := false
		switch {
		case strings.HasPrefix(text, "| Your prompt"):
			caption, expect = true, true
		case strings.HasPrefix(text, "| Explain this local demonstration"):
			prompt, expect = true, true
		case strings.HasPrefix(text, "Assistant"):
			reply = true
		case strings.Contains(text, "this simulated result"):
			answer = true
		default:
			continue
		}
		covered := 0
		for _, span := range frame.Styles {
			if span.Row != row {
				continue
			}
			if span.Start != covered || span.End <= span.Start || span.End > frame.Width || span.Foreground != "default" || span.Background != "default" || span.Reverse != expect {
				return false
			}
			covered = span.End
			if covered >= frame.Width-1 {
				break
			}
		}
		if covered < frame.Width-1 {
			return false
		}
	}
	return prompt && caption && reply && answer
}

func TestTranscriptBandPredicateRejectsMissingPaddingAndWrongContrast(t *testing.T) {
	frame := shellCapturedFrame{Width: 80, Height: 24, Rows: make([]string, 24)}
	frame.Rows[0] = "UCLI version"
	frame.Rows[1] = "read-only | effort auto"
	frame.Rows[2] = strings.Repeat("\u2500", 79)
	frame.Rows[3] = "| Your prompt"
	frame.Rows[4] = "| Explain this local demonstration"
	frame.Rows[5] = "Assistant \u00b7 demo/fast"
	frame.Rows[6] = "Your task and this simulated result are saved."
	frame.Rows[18] = "[OK] Ready"
	frame.Rows[19], frame.Rows[21] = frame.Rows[2], frame.Rows[2]
	frame.Rows[20] = ">"
	frame.Rows[22] = "Model auto \u00b7 /work"
	frame.Rows[23] = "Enter send | Tab actions"
	for row := 3; row <= 6; row++ {
		frame.Styles = append(frame.Styles, shellStyleSpan{Row: row, Start: 0, End: 79, Foreground: "default", Background: "default", Reverse: row < 5})
	}
	if !transcriptDefaultBandMatches(frame) {
		t.Fatal("valid paired-default band rejected")
	}
	for _, mutate := range []func(*shellCapturedFrame){
		func(f *shellCapturedFrame) { f.Styles = nil },
		func(f *shellCapturedFrame) { f.Styles[0].End = 13 },
		func(f *shellCapturedFrame) { f.Styles[1].Reverse = false },
		func(f *shellCapturedFrame) { f.Styles[1].Foreground = "red" },
		func(f *shellCapturedFrame) { f.Styles[1].Background = "red" },
		func(f *shellCapturedFrame) { f.Styles[2].Reverse = true },
		func(f *shellCapturedFrame) { f.Styles[3].Reverse = true },
	} {
		changed := frame
		changed.Styles = append([]shellStyleSpan(nil), frame.Styles...)
		mutate(&changed)
		if transcriptDefaultBandMatches(changed) {
			t.Fatal("missing/full-row or paired-contrast negative accepted")
		}
	}
}
