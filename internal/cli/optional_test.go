package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceCancellationRetainsCleanupFailure(t *testing.T) {
	failure := errors.New("injected owned-tree close failure")
	for _, reason := range []error{errVoiceDismissed, context.Canceled, context.DeadlineExceeded} {
		if got := interruptedVoice(reason, errors.Join(context.Canceled, context.Canceled)); got != reason {
			t.Fatalf("clean interruption changed: %v", got)
		}
		got := optionalProblem(interruptedVoice(reason, errors.Join(context.Canceled, failure)))
		if got == errVoiceDismissed || !errors.Is(got, failure) || problemFor(got).Exit != 9 {
			t.Fatalf("cleanup failure became clean cancellation: %v", got)
		}
	}
	if got := optionalProblem(errors.Join(context.Canceled, failure)); problemFor(got).Exit != 9 || !errors.Is(got, failure) {
		t.Fatalf("joined cleanup failure hidden: %v", got)
	}
}

func TestOptionalMetadataNeverStartsSetup(t *testing.T) {
	t.Setenv("UCLI_VOICE_PYTHON", filepath.Join(t.TempDir(), "never-execute.exe"))
	t.Setenv("UCLI_CLAUDE_EXE", filepath.Join(t.TempDir(), "never-execute-native.exe"))
	root := t.TempDir()
	home := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(home, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"voice", "models", "--json"}, {"runtime", "list", "--json"}, {"voice", "--help"}, {"runtime", "prepare", "claude", "--help"}} {
		code, text, diagnostic := call(t, home, "", arguments...)
		if code != 0 || text == "" || diagnostic != "" {
			t.Fatalf("%v: %d %q %q", arguments, code, text, diagnostic)
		}
	}
	got, err := os.ReadFile(home)
	if err != nil || string(got) != "preserve" {
		t.Fatalf("metadata touched state: %q %v", got, err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatal("metadata created an optional cache")
	}
}

func TestVoiceInputErrorsPrecedeDependencyAcquisition(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "untouched-home")
	file := filepath.Join(root, "unsupported.wav")
	if err := os.WriteFile(file, []byte(strings.Repeat("not PCM", 20)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "missing.wav"), file} {
		code, text, _ := call(t, home, "", "voice", "transcribe", path, "--json", "--offline", "--python", filepath.Join(root, "never-execute.exe"))
		if code == 0 || !strings.Contains(text, "voice_audio") {
			t.Fatalf("%d %s", code, text)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("bad audio prepared dependencies")
		}
	}
}

func TestVoiceModelAndLanguageErrorsPrecedeDependencyAcquisition(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "untouched-home")
	model := filepath.Join(root, "model")
	if err := os.Mkdir(model, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.json", "model.bin", "tokenizer.json", "vocabulary.txt"} {
		if err := os.WriteFile(filepath.Join(model, name), []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Truncate(filepath.Join(model, "config.json"), (1<<20)+1); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--model-dir", model},
		{"--language", strings.Repeat("e", 17)},
		{"--model", "whisper/tiny.en", "--language", "ko"},
	} {
		request := append([]string{"voice", "setup", "--json", "--python", filepath.Join(root, "never-execute.exe")}, args...)
		code, text, _ := call(t, home, "", request...)
		if code == 0 || strings.Contains(text, "voice_python") {
			t.Fatalf("dependency resolution preceded validation: %d %s", code, text)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("invalid model/language prepared dependencies")
		}
	}
}

func TestVoiceAndRuntimeFlagsPreserveIntent(t *testing.T) {
	for _, arguments := range [][]string{
		{"run", "--demo", "--offline", "text"},
		{"voice", "models", "--python", "python"},
		{"voice", "record", "--duration", "0"},
		{"voice", "record", "--duration", "121"},
		{"voice", "record", "--duration", "1.5"},
		{"voice", "transcribe", "-"},
		{"voice", "record", "--effort", "max"},
		{"voice", "record", "--model", "whisper/tiny.en", "--model-dir", "local"},
		{"runtime", "prepare"},
		{"runtime", "prepare", "claude", "--model", "demo/fast"},
	} {
		if _, err := parse(arguments); err == nil {
			t.Fatalf("invalid flags accepted: %v", arguments)
		}
	}
	for _, arguments := range [][]string{{"voice"}, {"voice", "transcribe", "local.wav", "--model", "whisper/tiny.en", "--offline"}, {"voice", "record", "--duration", "10", "--no-input", "--json"}, {"runtime", "prepare", "claude", "--offline"}} {
		if _, err := parse(arguments); err != nil {
			t.Fatalf("valid request rejected: %v %v", arguments, err)
		}
	}
}

func TestVoiceInsertionPreservesCompositionAndLiteralCommands(t *testing.T) {
	editor := textEditor{text: "Keep 中 context", cursor: len("Keep 中")}
	if err := insertVoiceText(&editor, "/send\nvoice text"); err != nil {
		t.Fatal(err)
	}
	if editor.text != "Keep 中\n/send\nvoice text\n context" {
		t.Fatalf("composition changed: %q", editor.text)
	}
	before := editor
	if err := insertVoiceText(&editor, ""); err != nil || editor != before {
		t.Fatalf("empty voice altered input: %+v %v", editor, err)
	}
	text := help("voice", false)
	for _, needle := range []string{"voice transcribe", "--model", "--offline", "--duration", "never submits"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("voice path undiscoverable: %s", needle)
		}
	}
}
