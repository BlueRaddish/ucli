package cli

import (
	"reflect"
	"testing"
)

func TestTerminalCompletionUsesSupportedRegistryAndWraps(t *testing.T) {
	var completion terminalCompletion
	completion.sync("/", 1, false)
	all := completion.result()
	if !all.Visible || all.Focus != 0 || len(all.Rows) == 0 || !reflect.DeepEqual(all.Rows, slashCommands) {
		t.Fatalf("slash did not expose the authoritative ordered catalog: %+v", all)
	}
	for _, row := range all.Rows {
		if row.Name == "" || row.Description == "" || row.Usage == "" {
			t.Fatalf("discoverable row lacks its registry metadata: %+v", row)
		}
	}
	completion.move(-1)
	if completion.result().Focus != len(all.Rows)-1 {
		t.Fatal("Up at the first row did not wrap to the last command")
	}
	completion.move(1)
	if completion.result().Focus != 0 {
		t.Fatal("Down at the last row did not wrap to the first command")
	}
	// A renderer may retain or decorate its copy without changing the catalog or
	// the next accepted command.
	all.Rows[0].Name = "/not-a-command"
	if completion.result().Rows[0].Name != slashCommands[0].Name {
		t.Fatal("a returned render snapshot mutated completion state")
	}
}

func TestTerminalCompletionTypingAndAcceptanceOnlyEditText(t *testing.T) {
	var editor textEditor
	var completion terminalCompletion
	for _, text := range []string{"/", "m", "o"} {
		if err := editor.apply(keyEvent{kind: keyText, text: text}); err != nil {
			t.Fatal(err)
		}
		completion.sync(editor.text, editor.cursor, false)
	}
	result := completion.result()
	if !result.Visible || len(result.Rows) != 1 || result.Rows[0].Name != "/models" {
		t.Fatalf("/mo did not filter the command-name prefix: %+v", result)
	}
	value, ok := completion.accept()
	if !ok || value != "/models " || editor.text != "/mo" {
		t.Fatalf("acceptance must return an edit without applying or dispatching it: %q %v", value, ok)
	}
	editor.set(value)
	completion.sync(editor.text, editor.cursor, false)
	if completion.result().Visible {
		t.Fatal("accepted trailing delimiter reopened token completion")
	}
	completion.sync("/models", len("/models"), false)
	result = completion.result()
	if !result.Visible || result.Rows[result.Focus].Name != "/models" {
		t.Fatal("caller cannot distinguish exact command input for Enter compatibility")
	}
}

func TestTerminalCompletionRetainsFocusAndDismissalUntilTextChanges(t *testing.T) {
	var completion terminalCompletion
	completion.sync("/", 1, false)
	completion.move(2)
	wanted := completion.result().Rows[completion.result().Focus].Name
	for range 10 { // Repaint and resize supply the same editor state.
		completion.sync("/", 1, false)
	}
	if completion.result().Rows[completion.result().Focus].Name != wanted {
		t.Fatal("unchanged input moved command focus")
	}
	completion.sync("/", 0, false)
	if completion.result().Visible {
		t.Fatal("cursor before the slash still offered a replacement")
	}
	completion.sync("/", 1, false)
	if completion.result().Rows[completion.result().Focus].Name != wanted {
		t.Fatal("cursor navigation erased the unchanged selection")
	}
	completion.dismiss()
	completion.sync("/", 1, false)
	completion.move(1)
	if completion.result().Visible {
		t.Fatal("repaint or arrows reopened an escaped query")
	}
	if _, ok := completion.accept(); ok {
		t.Fatal("a dismissed candidate was accepted")
	}
	completion.sync("/m", 2, false)
	if !completion.result().Visible || completion.result().Focus != 0 {
		t.Fatal("editing the query did not reopen at the first match")
	}
	completion.dismiss()
	completion.sync("", 0, false)
	completion.sync("/", 1, false)
	if !completion.result().Visible || completion.result().Focus != 0 {
		t.Fatal("a newly typed slash retained the previous dismissal")
	}
}

func TestTerminalCompletionNeverConsumesLiteralOrNonTokenInput(t *testing.T) {
	for _, input := range []struct {
		text    string
		cursor  int
		literal bool
	}{
		{"/models", 7, true},
		{"/models", 3, false},
		{"/models", -1, false},
		{"/models", 8, false},
		{"/models ", 8, false},
		{"/voice audio.wav", 16, false},
		{"/voice\taudio.wav", 16, false},
		{"/voice\u00a0audio.wav", len("/voice\u00a0audio.wav"), false},
		{"/models\n", 8, false},
		{"/models\r", 8, false},
		{"write /mo", 9, false},
		{" /mo", 4, false},
		{"/\x00", 2, false},
		{"/\xff", 2, false},
	} {
		t.Run(input.text, func(t *testing.T) {
			var completion terminalCompletion
			completion.sync(input.text, input.cursor, input.literal)
			completion.move(1)
			if completion.result().Visible {
				t.Fatalf("unexpected suggestions: %+v", input)
			}
			if _, ok := completion.accept(); ok {
				t.Fatalf("offered a replacement for protected input: %+v", input)
			}
		})
	}
}

func TestTerminalCompletionNoMatchAndBackspaceRecovery(t *testing.T) {
	var completion terminalCompletion
	var editor textEditor
	editor.set("/mozz")
	completion.sync(editor.text, editor.cursor, false)
	result := completion.result()
	if !result.Visible || len(result.Rows) != 0 || result.Focus != -1 {
		t.Fatalf("no-match query must remain a visible empty completion: %+v", result)
	}
	completion.move(-1)
	if _, ok := completion.accept(); ok {
		t.Fatal("no-match completion accepted an unrelated action")
	}
	for range 2 {
		if err := editor.apply(keyEvent{kind: keyBackspace}); err != nil {
			t.Fatal(err)
		}
		completion.sync(editor.text, editor.cursor, false)
	}
	result = completion.result()
	if !result.Visible || len(result.Rows) != 1 || result.Rows[0].Name != "/models" {
		t.Fatalf("backspace did not recover the command matches: %+v", result)
	}
}
