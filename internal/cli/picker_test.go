package cli

import (
	"fmt"
	"strings"
	"testing"
)

func pickerTranscript(p pickerState, rows int) string {
	var text strings.Builder
	for _, line := range p.frame(rows).Lines {
		text.WriteString(line.Text)
		text.WriteByte('\n')
	}
	return text.String()
}

func TestPickerFocusAndCancelPreserveActiveSelection(t *testing.T) {
	options := []pickerOption{
		{Value: "auto", Label: "Auto", Detail: "Choose within eligible routes"},
		{Value: "demo/deep", Label: "Deep", Detail: "Effort: low, medium, high"},
		{Value: "future/model", Label: "Future model", Detail: "Unavailable; provider is not admitted"},
	}
	p := newPicker("Model for the next submission", options, "demo/deep")
	if p.focused != "demo/deep" || !strings.Contains(pickerTranscript(p, 6), "> Deep (current)") {
		t.Fatal("opening did not focus and identify the current selection")
	}
	next, result := p.reduce(pickerAction{Kind: pickerMoveDown})
	if result != (pickerOutcome{}) || p.focused != "demo/deep" || next.focused != "future/model" || next.current != "demo/deep" {
		t.Fatal("moving focus committed a setting or changed the prior state")
	}
	text := pickerTranscript(next, 6)
	for _, want := range []string{"Current: Deep", "  Deep (current)", "> Future model", "Unavailable; provider is not admitted", "Enter select", "Esc cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing scope/readiness/controls %q: %s", want, text)
		}
	}
	canceled, result := next.reduce(pickerAction{Kind: pickerCancel})
	if !result.Canceled || result.Confirmed || result.Value != "" || canceled.current != p.current {
		t.Fatalf("cancel changed selection: %+v %+v", canceled, result)
	}
	_, result = canceled.reduce(pickerAction{Kind: pickerConfirm})
	if result != (pickerOutcome{}) {
		t.Fatal("a closed picker emitted another outcome")
	}
	// Readiness is visible, but merely pinning a future route is reversible.
	confirmed, result := next.reduce(pickerAction{Kind: pickerConfirm})
	if !result.Confirmed || result.Value != "future/model" || confirmed.current != "demo/deep" {
		t.Fatal("explicit confirmation failed or silently changed active state")
	}
	_, result = confirmed.reduce(pickerAction{Kind: pickerConfirm})
	if result != (pickerOutcome{}) {
		t.Fatal("repeated Enter emitted a duplicate confirmation")
	}
}

func TestPickerFilteringAndLiteralQueriesRecoverWithoutCommitting(t *testing.T) {
	p := newPicker("Choose a task", []pickerOption{
		{Value: "task-a", Label: "Repeated label", Detail: "First workspace"},
		{Value: "task-b", Label: "Repeated label", Detail: "Second workspace", SearchText: "archive"},
		{Value: "task-c", Label: "0 /older 한글", Detail: "Unsent draft 👩‍💻"},
	}, "task-b")
	if p.focused != "task-b" {
		t.Fatal("duplicate labels confused stable task identity")
	}
	for _, query := range []string{"SECOND", "archive", "task-b"} {
		filtered, result := p.reduce(pickerAction{Kind: pickerReplaceQuery, Text: query})
		if result != (pickerOutcome{}) || filtered.focused != "task-b" || len(filtered.matches()) != 1 || filtered.current != "task-b" {
			t.Fatalf("failed label/detail/ID filter %q", query)
		}
	}
	filtered, _ := p.reduce(pickerAction{Kind: pickerReplaceQuery, Text: "0 /older 한글"})
	if filtered.focused != "task-c" || filtered.query != "0 /older 한글" {
		t.Fatal("literal numeric/slash/Unicode query was interpreted as a command")
	}
	cleared, _ := filtered.reduce(pickerAction{Kind: pickerReplaceQuery})
	if cleared.focused != "task-c" || cleared.current != "task-b" {
		t.Fatal("clearing a filter lost a still-visible focus or committed it")
	}
	empty, _ := filtered.reduce(pickerAction{Kind: pickerReplaceQuery, Text: "/send no match"})
	empty, result := empty.reduce(pickerAction{Kind: pickerConfirm})
	if result != (pickerOutcome{}) || empty.done || !strings.Contains(pickerTranscript(empty, 6), "No matches. Edit the search") {
		t.Fatal("empty results committed an old focus or removed recovery")
	}
	empty, _ = empty.reduce(pickerAction{Kind: pickerMoveDown})
	recovered, _ := empty.reduce(pickerAction{Kind: pickerReplaceQuery})
	if recovered.focused != "task-b" {
		t.Fatal("clearing an unmatched query did not restore current choice")
	}
	_, result = filtered.reduce(pickerAction{Kind: pickerCancel})
	if !result.Canceled {
		t.Fatal("Escape with a nonempty query did not cancel immediately")
	}
}

func TestPickerViewportKeepsFocusVisibleAndWrapsNavigation(t *testing.T) {
	var options []pickerOption
	for i := range 12 {
		options = append(options, pickerOption{Value: fmt.Sprint(i), Label: fmt.Sprintf("Model %02d with a long comparable label", i)})
	}
	p := newPicker("Choose model", options, "0")
	p, _ = p.reduce(pickerAction{Kind: pickerMoveUp})
	if p.focused != "11" {
		t.Fatal("Up did not wrap from the first option to the last")
	}
	p, _ = p.reduce(pickerAction{Kind: pickerMoveDown})
	if p.focused != "0" {
		t.Fatal("Down did not wrap from the last option to the first")
	}
	p, _ = p.reduce(pickerAction{Kind: pickerMoveUp})
	if p.focused != "11" || p.current != "0" {
		t.Fatal("navigation committed a different current selection")
	}
	for _, rows := range []int{6, 3, 1, 99, 0} {
		frame := p.frame(rows)
		focused := 0
		for _, line := range frame.Lines {
			if line.Focused {
				focused++
				if !strings.HasPrefix(line.Text, "> Model 11") {
					t.Fatal("rendered focus disagrees with the navigated option")
				}
			}
		}
		if focused != 1 || frame.SearchRow != 2 || !strings.Contains(pickerTranscript(p, rows), "Current: Model 00") {
			t.Fatalf("viewport %d hid focus or current context", rows)
		}
	}
	p, _ = p.reduce(pickerAction{Kind: pickerHome})
	if p.focused != "0" {
		t.Fatal("Home did not reach the first match")
	}
	p, _ = p.reduce(pickerAction{Kind: pickerEnd})
	if p.focused != "11" {
		t.Fatal("End did not reach the last match")
	}
	// The picker owns an immutable snapshot even if its caller rebuilds catalog rows.
	options[11].Value = "changed-outside"
	_, result := p.reduce(pickerAction{Kind: pickerConfirm})
	if result.Value != "11" {
		t.Fatal("external option mutation changed the confirmed stable ID")
	}
}

func TestPickerEmptyCatalogAndMissingCurrentRemainExplicit(t *testing.T) {
	p := newPicker("Choose account", nil, "unlisted/account")
	text := pickerTranscript(p, 6)
	if !strings.Contains(text, "Current: unlisted/account") || !strings.Contains(text, "No choices available") {
		t.Fatal("empty catalog hid current scope or resembled a failed search")
	}
	for _, action := range []pickerActionKind{pickerMoveUp, pickerMoveDown, pickerHome, pickerEnd, pickerConfirm} {
		var result pickerOutcome
		p, result = p.reduce(pickerAction{Kind: action})
		if result != (pickerOutcome{}) || p.current != "unlisted/account" {
			t.Fatal("empty catalog emitted an invented choice")
		}
	}
	_, result := p.reduce(pickerAction{Kind: pickerCancel})
	if !result.Canceled {
		t.Fatal("empty catalog cannot be dismissed")
	}
}
