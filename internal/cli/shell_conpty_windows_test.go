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
	"strconv"
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
	Rows      []string         `json:"rows"`
	Width     int              `json:"width"`
	Height    int              `json:"height"`
	CursorX   int              `json:"cursor_x"`
	CursorY   int              `json:"cursor_y"`
	Colors    []string         `json:"foreground_colors"`
	RawBytes  int              `json:"raw_bytes"`
	RawSHA256 string           `json:"raw_sha256"`
	Styles    []shellStyleSpan `json:"style_spans"`
}

type shellStyleSpan struct {
	Row        int    `json:"row"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Foreground string `json:"foreground"`
	Background string `json:"background"`
	Reverse    bool   `json:"reverse"`
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
	for _, mode := range []string{"color", "NO_COLOR", "no-color-flag", "ascii-static"} {
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
			if mode == "ascii-static" {
				options = append(options, "--no-motion", "--ascii")
			}
			home := t.TempDir()
			cwd := filepath.Join(t.TempDir(), "framed-shell-workspace")
			if err := os.Mkdir(cwd, 0700); err != nil {
				t.Fatal(err)
			}
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
			p.send(t, "\x15")
			frame := awaitShellFrame(t, p, p.resizes, "initial", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 80, 24, "", ">") && shellFrameFooter(f, "auto", filepath.Base(cwd)) && shellFrameWelcomeCats(f, mode == "ascii-static")
			})
			assertShellColor(t, frame, mode)

			// Help is local output longer than the viewport, never a model request.
			mark := p.send(t, "/help\r")
			p.awaitVisible(t, mark, "/exit")
			awaitShellFrame(t, p, p.resizes, "output-overflow", func(f shellCapturedFrame) bool {
				return shellFrameBase(f, 80, 24) && strings.Contains(f.Rows[23], "Tab actions") && strings.Contains(strings.Join(f.Rows, "\n"), "PgUp/PgDn")
			})
			mark = p.send(t, "\x15/workflow missing-shell-plan.json\r")
			p.awaitVisible(t, mark, "Cannot read workflow file")
			frame = awaitShellFrame(t, p, p.resizes, "error-adjacent", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 80, 24, "[Error]", ">")
			})
			assertShellColor(t, frame, mode)

			const draft = "Unsent shell composition \u4e2d e\u0301"
			const visibleDraft = "Unsent shell composition \u4e2d"
			mark = p.send(t, "\x15\x1b[200~"+draft+"\x1b[201~")
			p.awaitVisible(t, mark, "Unsent shell composition")
			before := awaitShellFrame(t, p, p.resizes, "composition", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 80, 24, "[Error]", visibleDraft)
			})
			mark = p.send(t, "\t")
			p.awaitVisible(t, mark, "Actions")
			mark = p.send(t, "effort")
			p.awaitVisible(t, mark, "Search: effort")
			mark = p.send(t, "\r")
			p.awaitVisible(t, mark, "Choose effort")
			awaitShellFrame(t, p, p.resizes, "effort-overlay", func(f shellCapturedFrame) bool {
				return shellFrameComposer(f, 80, 24, "[Error]", []string{visibleDraft}, "Draft kept") && strings.Contains(strings.Join(f.Rows[3:18], "\n"), "Choose effort")
			})
			mark = p.send(t, "\x1b")
			p.awaitVisible(t, mark, "Tab actions")
			after := awaitShellFrame(t, p, p.resizes, "composition-restored", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 80, 24, "[Error]", visibleDraft) && f.CursorY == 20
			})
			if before.CursorX != after.CursorX {
				t.Fatalf("overlay changed cursor: %d -> %d", before.CursorX, after.CursorX)
			}
			p.resize(t, 60, 20)
			frame = awaitShellFrame(t, p, p.resizes, "resized", func(f shellCapturedFrame) bool {
				return shellFramePrompt(f, 60, 20, "[Error]", visibleDraft) && shellFrameFooter(f, "auto", filepath.Base(cwd)) && f.CursorY == 16
			})
			assertShellColor(t, frame, mode)
			// A second line grows upward while the footer and final editor row stay
			// fixed. This is literal composition, never a submitted command.
			const continuation = "\nSecond composer line"
			mark = p.send(t, "\x1b[200~"+continuation+"\x1b[201~")
			p.awaitVisible(t, mark, "Second composer line")
			awaitShellFrame(t, p, p.resizes, "multiline", func(f shellCapturedFrame) bool {
				return shellFrameComposer(f, 60, 20, "[Error]", []string{visibleDraft, "Second composer line"}, "Tab actions") &&
					shellFrameFooter(f, "auto", filepath.Base(cwd)) && f.CursorY == 16
			})
			got, attempts, err := a.Task(ctx, seed.ID)
			if err != nil || got.Draft != seed.Draft || got.Revision != seed.Revision || got.Selection != seed.Selection || len(attempts) != 0 {
				t.Fatalf("viewing changed durable task: %+v attempts=%d err=%v", got, len(attempts), err)
			}
			p.send(t, "\x03\x03")
			if code := p.wait(t); code != 130 {
				t.Fatalf("interrupt exit=%d", code)
			}
			got, attempts, err = a.Task(ctx, seed.ID)
			if err != nil || got.Draft != draft+continuation || got.Revision != seed.Revision+1 || got.Selection != seed.Selection || len(attempts) != 0 {
				t.Fatalf("exit lost composition or dispatched work: %+v attempts=%d err=%v", got, len(attempts), err)
			}
		})
	}
}

func shellFrameBase(f shellCapturedFrame, width, height int) bool {
	return width >= 40 && height >= 14 && f.Width == width && f.Height == height && len(f.Rows) == height &&
		strings.Contains(f.Rows[0], "UCLI") && strings.Contains(f.Rows[1], "read-only") && strings.Contains(f.Rows[1], "effort") &&
		shellFrameRule(f.Rows[2], width) && shellFrameRule(f.Rows[height-3], width) &&
		strings.Contains(f.Rows[height-2], "Model ")
}

func shellFramePrompt(f shellCapturedFrame, width, height int, notice, input string) bool {
	return shellFrameComposer(f, width, height, notice, []string{input}, "Tab actions")
}

func shellFrameComposer(f shellCapturedFrame, width, height int, notice string, inputs []string, hint string) bool {
	if !shellFrameBase(f, width, height) || len(inputs) < 1 || len(inputs) > height-8 || !strings.Contains(f.Rows[height-1], hint) {
		return false
	}
	separator := height - len(inputs) - 4
	if !shellFrameRule(f.Rows[separator], width) || !strings.Contains(f.Rows[separator-1], notice) {
		return false
	}
	for i, input := range inputs {
		if !strings.Contains(f.Rows[separator+1+i], input) {
			return false
		}
	}
	return true
}

func shellFrameRule(row string, width int) bool {
	return strings.TrimSpace(row) == strings.Repeat("\u2500", width-1)
}

func shellFrameFooter(f shellCapturedFrame, model, pathTail string) bool {
	return len(f.Rows) >= 2 && strings.Contains(f.Rows[len(f.Rows)-2], "Model "+model) &&
		strings.Contains(f.Rows[len(f.Rows)-2], pathTail)
}

func shellColorMatches(f shellCapturedFrame, mode string) bool {
	return (len(f.Colors) == 0) == (mode != "color" && mode != "ascii-static")
}

func shellFrameWelcomeCats(f shellCapturedFrame, ascii bool) bool {
	if f.Height < 14 || len(f.Rows) != f.Height {
		return false
	}
	face := "(^.\u2a4a.^)"
	if ascii {
		face = "(^._.^)"
		if strings.ContainsAny(strings.Join(f.Rows, "\n"), "\u2a4a\u0e05\u1a10\ufecc") {
			return false
		}
	}
	return strings.Contains(f.Rows[0], face) && strings.Contains(f.Rows[f.Height-6], face)
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
	f.Rows[0], f.Rows[1], f.Rows[2] = "UCLI version", "read-only | effort auto | account auto", strings.Repeat("\u2500", 79)
	f.Rows[18], f.Rows[19], f.Rows[20], f.Rows[21] = "[Error] fixture", strings.Repeat("\u2500", 79), "> kept", strings.Repeat("\u2500", 79)
	f.Rows[22], f.Rows[23] = "Model auto · /work/framed-shell-workspace", "Tab actions"
	if !shellFrameBase(f, 80, 24) {
		t.Fatal("valid frame rejected")
	}
	if !shellFramePrompt(f, 80, 24, "[Error]", "> kept") || !shellFrameFooter(f, "auto", "framed-shell-workspace") {
		t.Fatal("valid input/notice rejected")
	}
	if shellFrameRule(strings.Repeat("-", 79), 80) || shellFrameRule(strings.Repeat("\u2500", 78), 80) {
		t.Fatal("discontinuous or short rule accepted")
	}
	for _, row := range []int{0, 1, 2, 18, 19, 20, 21, 22, 23} {
		t.Run("missing-row-"+strconv.Itoa(row), func(t *testing.T) {
			moved := f
			moved.Rows = append([]string(nil), f.Rows...)
			moved.Rows[3], moved.Rows[row] = moved.Rows[row], ""
			if shellFramePrompt(moved, 80, 24, "[Error]", "> kept") {
				t.Fatalf("required row %d accepted after moving into body", row)
			}
		})
	}
	wrong := f
	wrong.Rows = append([]string(nil), f.Rows...)
	wrong.Rows[22] = "Model other · /work/foreign"
	if shellFrameFooter(wrong, "auto", "framed-shell-workspace") {
		t.Fatal("wrong requested model and lost path tail accepted")
	}
	multi := f
	multi.Rows = append([]string(nil), f.Rows...)
	multi.Rows[17], multi.Rows[18], multi.Rows[19], multi.Rows[20] = "[Error] fixture", strings.Repeat("\u2500", 79), "> first", "  second"
	if !shellFrameComposer(multi, 80, 24, "[Error]", []string{"> first", "second"}, "Tab actions") || shellFramePrompt(multi, 80, 24, "[Error]", "second") {
		t.Fatal("multiline upward expansion did not retain distinct separator/notice coordinates")
	}
	overlay := f
	overlay.Rows = append([]string(nil), f.Rows...)
	overlay.Rows[23] = "Draft kept · finish this panel to edit"
	if !shellFrameComposer(overlay, 80, 24, "[Error]", []string{"> kept"}, "Draft kept") || shellFramePrompt(overlay, 80, 24, "[Error]", "> kept") {
		t.Fatal("overlay and editable-composer hints were not distinguished")
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
	f.Rows[0], f.Rows[18] = "(^._.^) UCLI version", "[Info] (^._.^)  Ready"
	if !shellFrameWelcomeCats(f, true) {
		t.Fatal("static ASCII welcome cats rejected")
	}
	f.Rows[18] = "[Info] Ready"
	if shellFrameWelcomeCats(f, true) {
		t.Fatal("missing welcome notice cat accepted")
	}
	f.Rows[18] = "[Info] (^.\u2a4a.^)  Ready"
	if shellFrameWelcomeCats(f, true) {
		t.Fatal("Unicode cat accepted in ASCII mode")
	}
}
