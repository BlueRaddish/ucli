package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

type shellResize struct {
	Offset int `json:"offset"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type shellCapturedFrame struct {
	Rows      []string `json:"rows"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	CursorX   int      `json:"cursor_x"`
	CursorY   int      `json:"cursor_y"`
	Colors    []string `json:"foreground_colors"`
	RawBytes  int      `json:"raw_bytes"`
	RawSHA256 string   `json:"raw_sha256"`
}

// This opt-in check consumes an actual packaged process and checks reconstructed
// screen coordinates, not substring order in a stream of partial redraws.
// QA pyte is optional, external to the product, and never installed by this test.
func TestPackagedConPTYFixedShell(t *testing.T) {
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" || os.Getenv("UCLI_TERMINAL_QA_DEPS") == "" {
		t.Skip("set UCLI_CONPTY_BINARY and UCLI_TERMINAL_QA_DEPS for packaged screen verification")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	t.Logf("packaged shell sha256=%s", hex.EncodeToString(hash[:]))
	for _, mode := range []string{"color", "NO_COLOR", "no-color-flag"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("NO_COLOR", "")
			options := []string{}
			if mode == "NO_COLOR" {
				t.Setenv("NO_COLOR", "1")
			}
			if mode == "no-color-flag" {
				options = append(options, "--no-color")
			}
			home, cwd := t.TempDir(), t.TempDir()
			a, err := app.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			ctx := context.Background()
			seed, err := a.Draft(ctx, "", "Saved shell fixture", cwd, core.Defaults())
			if err != nil {
				t.Fatal(err)
			}
			p := startConPTYOptions(t, binary, home, cwd, false, options...)
			p.awaitVisible(t, 0, "Tab actions")
			frame := awaitShellFrame(t, p, p.resizes, "initial", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 80, 24, "", ">")
			})
			assertShellColor(t, frame, mode)

			// Help is local output longer than the viewport, never a model request.
			mark := p.send(t, "/help\r")
			p.awaitVisible(t, mark, "/exit")
			awaitShellFrame(t, p, p.resizes, "output-overflow", func(f shellCapturedFrame) bool {
				return shellFrameBase(f, 80, 24) && strings.Contains(strings.Join(f.Rows, "\n"), "PgUp/PgDn")
			})
			mark = p.send(t, "/workflow missing-shell-plan.json\r")
			p.awaitVisible(t, mark, "Cannot read workflow file")
			frame = awaitShellFrame(t, p, p.resizes, "error-adjacent", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 80, 24, "[Error]", ">")
			})
			assertShellColor(t, frame, mode)

			const draft = "Unsent shell composition \u4e2d e\u0301"
			const visibleDraft = "Unsent shell composition \u4e2d"
			mark = p.send(t, "\x1b[200~"+draft+"\x1b[201~")
			p.awaitVisible(t, mark, "Unsent shell composition")
			before := awaitShellFrame(t, p, p.resizes, "composition", func(f shellCapturedFrame) bool {
				return shellFrameBase(f, 80, 24) && strings.Contains(f.Rows[22], visibleDraft)
			})
			mark = p.send(t, "\t")
			p.awaitVisible(t, mark, "Actions")
			mark = p.send(t, "effort")
			p.awaitVisible(t, mark, "Search: effort")
			mark = p.send(t, "\r")
			p.awaitVisible(t, mark, "Choose effort")
			awaitShellFrame(t, p, p.resizes, "effort-overlay", func(f shellCapturedFrame) bool {
				return shellFrameBase(f, 80, 24) && strings.Contains(strings.Join(f.Rows[3:21], "\n"), "Choose effort") && strings.Contains(f.Rows[22], visibleDraft)
			})
			mark = p.send(t, "\x1b")
			p.awaitVisible(t, mark, "Tab actions")
			after := awaitShellFrame(t, p, p.resizes, "composition-restored", func(f shellCapturedFrame) bool {
				return shellFrameBase(f, 80, 24) && strings.Contains(f.Rows[22], visibleDraft) && f.CursorY == 22
			})
			if before.CursorX != after.CursorX {
				t.Fatalf("overlay changed cursor: %d -> %d", before.CursorX, after.CursorX)
			}
			p.resize(t, 60, 20)
			frame = awaitShellFrame(t, p, p.resizes, "resized", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 60, 20, "[Error]", visibleDraft) && f.CursorY == 18
			})
			assertShellColor(t, frame, mode)
			got, attempts, err := a.Task(ctx, seed.ID)
			if err != nil || got.Draft != seed.Draft || got.Revision != seed.Revision || got.Selection != seed.Selection || len(attempts) != 0 {
				t.Fatalf("viewing changed durable task: %+v attempts=%d err=%v", got, len(attempts), err)
			}
			p.send(t, "\x03")
			if code := p.wait(t); code != 130 {
				t.Fatalf("interrupt exit=%d", code)
			}
			got, attempts, err = a.Task(ctx, seed.ID)
			if err != nil || got.Draft != seed.Draft+"\n"+draft || got.Revision != seed.Revision+1 || got.Selection != seed.Selection || len(attempts) != 0 {
				t.Fatalf("exit lost composition or dispatched work: %+v attempts=%d err=%v", got, len(attempts), err)
			}
		})
	}
}

func shellFrameBase(f shellCapturedFrame, width, height int) bool {
	return f.Width == width && f.Height == height && len(f.Rows) == height && strings.Contains(f.Rows[0], "UCLI") && strings.Contains(f.Rows[1], "Location") && strings.Contains(f.Rows[2], "Model")
}

func shellFramePrompt(f shellCapturedFrame, width, height int, notice, input string) bool {
	return shellFrameBase(f, width, height) && strings.Contains(f.Rows[height-3], notice) && strings.Contains(f.Rows[height-2], input) && strings.Contains(f.Rows[height-1], "Tab actions")
}

func shellColorMatches(f shellCapturedFrame, mode string) bool {
	return (len(f.Colors) == 0) == (mode != "color")
}

func assertShellColor(t *testing.T, f shellCapturedFrame, mode string) {
	t.Helper()
	if !shellColorMatches(f, mode) {
		t.Fatalf("%s foreground color mismatch: %v", mode, f.Colors)
	}
}

func awaitShellFrame(t *testing.T, p *pseudoTerminal, resizes []shellResize, label string, matches func(shellCapturedFrame) bool) shellCapturedFrame {
	t.Helper()
	if resizes == nil {
		resizes = []shellResize{}
	}
	deadline := time.Now().Add(8 * time.Second)
	var frame shellCapturedFrame
	for {
		raw := p.snapshot()
		input, err := json.Marshal(map[string]any{"raw": raw, "resizes": resizes})
		if err != nil {
			t.Fatal(err)
		}
		python := os.Getenv("UCLI_TERMINAL_QA_PYTHON")
		if python == "" {
			python = "python"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, python, filepath.Join("..", "..", "tools", "check_terminal_frame.py"), "--deps", os.Getenv("UCLI_TERMINAL_QA_DEPS"))
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("frame helper: %v %s", err, out)
		}
		if err := json.Unmarshal(out, &frame); err != nil {
			t.Fatalf("frame helper JSON: %v %s", err, out)
		}
		if matches(frame) {
			p.observations = append(p.observations, map[string]any{"needle": "screen:" + label, "observed_offset": len(raw), "scope": "ANSI-emulated captured frame", "width": frame.Width, "height": frame.Height, "raw_sha256": frame.RawSHA256})
			if evidence := os.Getenv("UCLI_CONPTY_EVIDENCE"); evidence != "" {
				if err := os.MkdirAll(evidence, 0700); err != nil {
					t.Fatal(err)
				}
				name := strings.ReplaceAll(t.Name(), "/", "-") + "-" + label + ".frame.json"
				proof := map[string]any{"frame": frame, "resizes": resizes, "observed_offset": len(raw)}
				data, _ := json.MarshalIndent(proof, "", "  ")
				file, err := os.OpenFile(filepath.Join(evidence, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.Write(append(data, '\n'))
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				if writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			return frame
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen %s mismatch:\n%s\ncursor=%d,%d", label, strings.Join(frame.Rows, "\n"), frame.CursorX, frame.CursorY)
		}
		select {
		case <-p.changed:
		case <-time.After(40 * time.Millisecond):
		}
	}
}

// A passing assertion must not simply accept any header-looking bytes. These
// controls exercise the same predicate used by the packaged observation loop.
func TestShellFramePredicateRejectsMovedHeaderAndWrongSize(t *testing.T) {
	f := shellCapturedFrame{Width: 80, Height: 24, Rows: make([]string, 24)}
	f.Rows[0], f.Rows[1], f.Rows[2] = "UCLI version", "Location fixture", "Model auto"
	if !shellFrameBase(f, 80, 24) {
		t.Fatal("valid frame rejected")
	}
	f.Rows[0], f.Rows[3] = "", "UCLI version"
	if shellFrameBase(f, 80, 24) {
		t.Fatal("scrolled header accepted")
	}
	f.Rows[0] = "UCLI version"
	f.Rows[21], f.Rows[22], f.Rows[23] = "[Error] fixture", "> kept", "Tab actions"
	if !shellFramePrompt(f, 80, 24, "[Error]", "> kept") {
		t.Fatal("valid input/notice rejected")
	}
	f.Rows[21], f.Rows[20] = "", "[Error] fixture"
	if shellFramePrompt(f, 80, 24, "[Error]", "> kept") {
		t.Fatal("displaced notice accepted")
	}
	f.Rows[21] = "[Error] fixture"
	f.Rows[22], f.Rows[19] = "", "> kept"
	if shellFramePrompt(f, 80, 24, "[Error]", "> kept") {
		t.Fatal("displaced composer accepted")
	}
	if !shellColorMatches(f, "NO_COLOR") || shellColorMatches(f, "color") {
		t.Fatal("empty color classification wrong")
	}
	f.Colors = []string{"ff0000"}
	if shellColorMatches(f, "NO_COLOR") || !shellColorMatches(f, "color") {
		t.Fatal("foreground color control accepted")
	}
	if shellFrameBase(f, 60, 20) {
		t.Fatal("wrong dimensions accepted")
	}
}
