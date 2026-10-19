package cli

import (
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

func TestEditorGraphemeEditing(t *testing.T) {
	for _, cluster := range []string{"e\u0301", "👩🏽‍💻", "🇰🇷", "가", "中"} {
		t.Run(cluster, func(t *testing.T) {
			e := textEditor{}
			e.set("a" + cluster + "z")
			_ = e.apply(keyEvent{kind: keyLeft})
			_ = e.apply(keyEvent{kind: keyBackspace})
			if e.text != "az" || e.cursor != 1 {
				t.Fatalf("backspace split cluster: %#v", e)
			}
			e.set("a" + cluster + "z")
			_ = e.apply(keyEvent{kind: keyHome})
			_ = e.apply(keyEvent{kind: keyRight})
			_ = e.apply(keyEvent{kind: keyDelete})
			if e.text != "az" || e.cursor != 1 {
				t.Fatalf("delete split cluster: %#v", e)
			}
		})
	}
	e := textEditor{}
	e.set("ex")
	_ = e.apply(keyEvent{kind: keyLeft})
	_ = e.apply(keyEvent{kind: keyText, text: "\u0301"})
	_ = e.apply(keyEvent{kind: keyBackspace})
	if e.text != "x" {
		t.Fatalf("inserted accent was separated: %q", e.text)
	}
}

func TestEditorMultilineAndRejectedInput(t *testing.T) {
	e := textEditor{}
	if err := e.apply(keyEvent{kind: keyPaste, text: "one\r\ntwo\rthree"}); err != nil {
		t.Fatal(err)
	}
	if e.text != "one\ntwo\nthree" {
		t.Fatal(e.text)
	}
	_ = e.apply(keyEvent{kind: keyHome})
	_ = e.apply(keyEvent{kind: keyText, text: "X"})
	_ = e.apply(keyEvent{kind: keyKillEnd})
	if e.text != "one\ntwo\nX" {
		t.Fatal(e.text)
	}
	for _, input := range []string{"\x00", string([]byte{255}), strings.Repeat("x", core.MaxInputBytes)} {
		before := e
		if err := e.apply(keyEvent{kind: keyPaste, text: input}); err == nil {
			t.Fatal("accepted invalid input")
		}
		if e != before {
			t.Fatal("rejected input changed draft")
		}
	}
}

func TestTerminalCellsAndUntrustedText(t *testing.T) {
	if got := clipCells("a中👩🏽‍💻z", 4); got != "a中" {
		t.Fatalf("split/wrapped grapheme: %q", got)
	}
	if got := clipCells("e\u0301x", 1); got != "e\u0301" {
		t.Fatal(got)
	}
	text := inlineText("\x1b[2Jsecret\nnext\tcell")
	if strings.ContainsAny(text, "\x1b\n\t") {
		t.Fatalf("unsafe terminal content: %q", text)
	}
}
