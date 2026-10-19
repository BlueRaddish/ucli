package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceSetupHistoryRemembersChoicesWithoutClobber(t *testing.T) {
	home := t.TempDir()
	p := invocation{voiceDuration: 30, set: map[string]bool{}}
	prepared := voicePreparation{Python: filepath.Join(home, "python.exe"), SiteDir: filepath.Join(home, "site"), Model: "local", ModelDir: filepath.Join(home, "model")}
	if err := saveVoiceProfile(context.Background(), home, p, prepared); err != nil {
		t.Fatal(err)
	}
	p.voiceDuration = 10
	p.language = "ko"
	if err := saveVoiceProfile(context.Background(), home, p, prepared); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(home, "voice-profiles"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("history overwritten: %v %v", entries, err)
	}
	loaded, err := applyVoiceProfile(context.Background(), invocation{voiceDuration: 30, set: map[string]bool{}}, home)
	if err != nil || loaded.voicePython != prepared.Python || loaded.voiceSite != prepared.SiteDir || loaded.voiceModelDir != prepared.ModelDir || loaded.language != "ko" || loaded.voiceDuration != 10 {
		t.Fatalf("choice lost: %+v %v", loaded, err)
	}
	override := invocation{voicePython: filepath.Join(home, "different.exe"), voiceDuration: 5, language: "en", set: map[string]bool{"model": true, "duration": true}}
	override.selection.Model = "whisper/tiny.en"
	loaded, err = applyVoiceProfile(context.Background(), override, home)
	if err != nil || loaded.voicePython != override.voicePython || loaded.voiceSite != "" || loaded.voiceModelDir != "" || loaded.voiceDuration != 5 || loaded.language != "en" {
		t.Fatalf("explicit settings replaced: %+v %v", loaded, err)
	}
}

func TestVoiceProfileRejectsForeignAmbiguousAndCanceledWrites(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "voice-profiles")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(directory, "profile-99999999999999999999-foreign.json")
	bytes := []byte(`{"version":1,"version":1,"kind":"ucli-voice-profile"}`)
	if err := os.WriteFile(foreign, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadVoiceProfile(context.Background(), home); err == nil {
		t.Fatal("ambiguous foreign profile selected")
	}
	prepared := voicePreparation{Python: filepath.Join(home, "python.exe"), Model: "whisper/tiny.en"}
	if err := saveVoiceProfile(context.Background(), home, invocation{voiceDuration: 30}, prepared); err == nil {
		t.Fatal("foreign profile bypassed")
	}
	got, _ := os.ReadFile(foreign)
	if string(got) != string(bytes) {
		t.Fatal("foreign profile changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cleanHome := filepath.Join(t.TempDir(), "absent")
	if err := saveVoiceProfile(ctx, cleanHome, invocation{voiceDuration: 30}, prepared); err == nil {
		t.Fatal("canceled setup saved settings")
	}
	if _, err := os.Stat(cleanHome); !os.IsNotExist(err) {
		t.Fatal("canceled setup created a profile home")
	}
}

func TestVoiceProfileUnknownFilesStayUntouched(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "voice-profiles")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(directory, "unrelated.txt")
	if err := os.WriteFile(foreign, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared := voicePreparation{Python: filepath.Join(home, "python.exe"), Model: "whisper/tiny.en"}
	if err := saveVoiceProfile(context.Background(), home, invocation{voiceDuration: 30}, prepared); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(foreign)
	if strings.TrimSpace(string(got)) != "foreign" {
		t.Fatal("unrelated file changed")
	}
}

func TestVoiceProfileRejectsValidBodyUnderForeignName(t *testing.T) {
	home := t.TempDir()
	prepared := voicePreparation{Python: filepath.Join(home, "python.exe"), Model: "whisper/tiny.en"}
	if err := saveVoiceProfile(context.Background(), home, invocation{voiceDuration: 30}, prepared); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, "voice-profiles")
	entries, _ := os.ReadDir(directory)
	foreign := filepath.Join(directory, "profile-z.json")
	if err := os.Rename(filepath.Join(directory, entries[0].Name()), foreign); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(foreign)
	if _, err := loadVoiceProfile(context.Background(), home); err == nil {
		t.Fatal("foreign filename selected executable settings")
	}
	if err := saveVoiceProfile(context.Background(), home, invocation{voiceDuration: 30}, prepared); err == nil {
		t.Fatal("foreign filename accepted during setup")
	}
	after, _ := os.ReadFile(foreign)
	if string(before) != string(after) {
		t.Fatal("foreign file changed")
	}
}

func TestVoiceSetupNeverPublishesAnUnreadableCombinedProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "untouched")
	long := filepath.Join(t.TempDir(), strings.Repeat("x", 24000))
	prepared := voicePreparation{Python: long, SiteDir: long, Model: "local", ModelDir: long}
	if err := saveVoiceProfile(context.Background(), home, invocation{voiceDuration: 30}, prepared); err == nil {
		t.Fatal("published profile beyond the reader's record limit")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("oversized profile created history")
	}
}

func TestVoiceSetupPreservesOwnedDependencyResolution(t *testing.T) {
	home := t.TempDir()
	prepared := voicePreparation{Python: filepath.Join(home, "python.exe"), SiteDir: filepath.Join(home, "owned-site"), RuntimeSource: "prepared-site", Model: "whisper/tiny.en"}
	if err := saveVoiceProfile(context.Background(), home, invocation{voiceDuration: 30}, prepared); err != nil {
		t.Fatal(err)
	}
	loaded, err := applyVoiceProfile(context.Background(), invocation{}, home)
	if err != nil || loaded.voicePython != prepared.Python || loaded.voiceSite != "" {
		t.Fatalf("owned site became a manifest-bypassing external override: %+v %v", loaded, err)
	}
}
