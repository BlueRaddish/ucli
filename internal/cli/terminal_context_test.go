package cli

import (
	"strings"
	"testing"
)

func TestTerminalContextBoundaryRemainsVisibleBelowInput(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 20}} {
		_, program := shellHarness(t, size[0], size[1], false)
		program.model.shell.chrome = terminalChrome{Model: "codex/example", Permission: "read-only", Account: "Work"}
		frame := program.model.shellFrame(size[0], size[1])
		text := frame.Lines[size[1]-2].Text
		if !strings.Contains(text, "fresh native context") || !strings.Contains(text, "Account Work") {
			t.Fatalf("native context/account boundary disappeared at %v: %q", size, text)
		}
		program.model.shell.chrome.Demo = true
		frame = program.model.shellFrame(size[0], size[1])
		text = frame.Lines[size[1]-2].Text
		if !strings.HasPrefix(text, "DEMO") || strings.Contains(text, "native context") {
			t.Fatalf("local demo is mislabeled as native at %v: %q", size, text)
		}
	}
}
