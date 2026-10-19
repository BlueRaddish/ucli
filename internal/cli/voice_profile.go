package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ucli.local/ucli/internal/artifact"
	"ucli.local/ucli/internal/core"
)

type voiceProfile struct {
	Version  int    `json:"version"`
	Kind     string `json:"kind"`
	Python   string `json:"python"`
	SiteDir  string `json:"site_dir,omitempty"`
	Model    string `json:"model"`
	ModelDir string `json:"model_dir,omitempty"`
	Language string `json:"language"`
	Duration int    `json:"duration"`
}

func profileProblem() error {
	return core.Problem("voice_profile_unsafe", "Voice setup history is invalid or linked. Inspect it before running voice setup again; existing files were kept.", 3)
}

func validVoiceProfile(p voiceProfile) bool {
	if p.Version != 1 || p.Kind != "ucli-voice-profile" || !filepath.IsAbs(p.Python) || (p.SiteDir != "" && !filepath.IsAbs(p.SiteDir)) || p.Model == "" || p.Duration < 1 || p.Duration > 120 || len(p.Language) > 16 {
		return false
	}
	if p.Model == "local" && !filepath.IsAbs(p.ModelDir) || p.Model != "local" && p.ModelDir != "" {
		return false
	}
	for _, value := range []string{p.Python, p.SiteDir, p.Model, p.ModelDir, p.Language} {
		if len(value) > 32768 || strings.ContainsRune(value, 0) {
			return false
		}
	}
	return true
}

func voiceProfileRevision(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "profile-") || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	parts := strings.TrimSuffix(strings.TrimPrefix(name, "profile-"), ".json")
	number, nonce, ok := strings.Cut(parts, "-")
	if !ok || len(number) != 20 || len(nonce) != len("choice_")+32 || !strings.HasPrefix(nonce, "choice_") {
		return 0, false
	}
	for _, c := range strings.TrimPrefix(nonce, "choice_") {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return 0, false
		}
	}
	revision, err := strconv.ParseUint(number, 10, 64)
	return revision, err == nil && revision > 0
}

// Setup history is immutable. The highest completed revision wins (simultaneous
// revisions use the random ID as a deterministic tie-breaker). No system-clock
// ordering or overwriting is needed. If this
// rarely edited history grows, replace this O(directory entries), bounded-memory
// scan with the shared metadata store.
// It is read only on a voice request, never ordinary UCLI startup or discovery.
func loadVoiceProfile(ctx context.Context, home string) (*voiceProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := filepath.Join(home, "voice-profiles")
	if _, err := artifact.New(root); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, profileProblem()
	}
	directory, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	name := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := directory.ReadDir(100)
		for _, entry := range entries {
			candidate := entry.Name()
			if strings.HasPrefix(candidate, "profile-") && strings.HasSuffix(candidate, ".json") {
				if _, valid := voiceProfileRevision(candidate); !valid {
					return nil, profileProblem()
				}
				if candidate > name {
					name = candidate
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if name == "" {
		return nil, nil
	}
	path := filepath.Join(root, name)
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, profileProblem()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p voiceProfile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF || !validVoiceProfile(p) {
		return nil, profileProblem()
	}
	canonical, _ := json.Marshal(p)
	// Only our own canonical records are accepted: duplicate keys, alternate
	// spelling and manual ambiguous edits must not select executable paths.
	if !bytes.Equal(bytes.TrimSpace(data), canonical) {
		return nil, profileProblem()
	}
	return &p, nil
}

func applyVoiceProfile(ctx context.Context, p invocation, home string) (invocation, error) {
	profile, err := loadVoiceProfile(ctx, home)
	if err != nil || profile == nil {
		return p, err
	}
	if p.voicePython == "" && os.Getenv("UCLI_VOICE_PYTHON") == "" {
		p.voicePython = profile.Python
		if p.voiceSite == "" && os.Getenv("UCLI_VOICE_SITE") == "" {
			p.voiceSite = profile.SiteDir
		}
	}
	if !p.set["model"] && p.voiceModelDir == "" && os.Getenv("UCLI_VOICE_MODEL_DIR") == "" {
		if profile.Model == "local" {
			p.voiceModelDir = profile.ModelDir
		} else {
			p.selection.Model = profile.Model
		}
	}
	if p.language == "" {
		p.language = profile.Language
	}
	if !p.set["duration"] {
		p.voiceDuration = profile.Duration
	}
	return p, nil
}

func saveVoiceProfile(ctx context.Context, home string, p invocation, prepared voicePreparation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := loadVoiceProfile(ctx, home); err != nil {
		return err
	}
	profile := voiceProfile{Version: 1, Kind: "ucli-voice-profile", Python: prepared.Python, SiteDir: prepared.SiteDir, Model: prepared.Model, Language: p.language, Duration: p.voiceDuration}
	if prepared.RuntimeSource == "prepared-site" {
		// Keep UCLI-owned dependencies on voiceenv's manifest-verified path.
		// Remembering their directory as an external override would bypass it.
		profile.SiteDir = ""
	}
	if profile.Language == "" {
		profile.Language = "en"
	}
	if profile.Model == "local" {
		profile.ModelDir = prepared.ModelDir
	}
	if !validVoiceProfile(profile) {
		return profileProblem()
	}
	data, _ := json.Marshal(profile)
	if len(data)+1 > 65536 { // Match the reader's complete-record bound.
		return profileProblem()
	}
	root := filepath.Join(home, "voice-profiles")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if _, err := artifact.New(root); err != nil {
		return err
	}
	stage, err := os.CreateTemp(root, ".pending-profile-")
	if err != nil {
		return err
	}
	stageName := stage.Name()
	defer os.Remove(stageName)
	if _, err = stage.Write(append(data, '\n')); err == nil {
		err = stage.Sync()
	}
	if closeErr := stage.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var revision uint64 = 1
	directory, err := os.Open(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	for {
		entries, err := directory.ReadDir(100)
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, "profile-") && strings.HasSuffix(name, ".json") {
				previous, valid := voiceProfileRevision(name)
				if !valid || previous == ^uint64(0) {
					return profileProblem()
				}
				if previous >= revision {
					revision = previous + 1
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	name := fmt.Sprintf("profile-%020d-%s.json", revision, core.NewID("choice"))
	return os.Link(stageName, filepath.Join(root, name))
}
