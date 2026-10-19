package codex

import (
	"strings"
	"testing"
)

func TestWarningIsInformationalUntilTerminal(t *testing.T) {
	for _, tc := range []struct{ mode, state string }{
		{"warning-complete", "complete"}, {"warning-omitted", "complete"}, {"warning-null", "complete"}, {"warning-failed", "failed"}, {"warning-eof", "unknown"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			out, output, err := runFixture(t, tc.mode)
			if out.State != tc.state || (err == nil) != (tc.state == "complete") {
				t.Fatalf("warning settled incorrectly: %+v %v", out, err)
			}
			note := "A native warning was observed; private warning text was withheld."
			if strings.Count(out.Message, note) != 1 || strings.Contains(out.Message, "SECRET") || strings.Contains(output, "SECRET") {
				t.Fatalf("warning absent or private text escaped: %+v", out)
			}
			if tc.state == "complete" && output != "hello \u4e16\u754c" {
				t.Fatal("real output was not retained")
			}
		})
	}
}
func TestMalformedAndMismatchedWarningsCannotComplete(t *testing.T) {
	for _, mode := range []string{"warning-wrong-thread", "warning-wrong-turn", "warning-invalid-thread", "warning-invalid-message", "warning-missing-message"} {
		t.Run(mode, func(t *testing.T) {
			out, output, err := runFixture(t, mode)
			if out.State != "unknown" || err == nil || output != "" || strings.Contains(out.Message, "SECRET") || strings.Contains(out.Message, "A native warning was observed") {
				t.Fatal(out, err)
			}
		})
	}
}
