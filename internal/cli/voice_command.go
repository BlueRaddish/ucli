package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/assets"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/voice"
	"ucli.local/ucli/internal/voiceenv"
)

var errVoiceDismissed = errors.New("voice dismissed")

func interruptedVoice(reason, outcome error) error {
	if onlyContextError(outcome) {
		return reason
	}
	return errors.Join(core.Problem("voice_cleanup_unconfirmed", "Voice cancellation had an additional failure; cleanup was not confirmed. No transcript was accepted.", 9), outcome)
}

type voicePreparation struct {
	Python           string            `json:"python"`
	SiteDir          string            `json:"site_dir,omitempty"`
	RuntimeSource    string            `json:"runtime_source"`
	Model            string            `json:"model"`
	ModelDir         string            `json:"model_dir"`
	Versions         map[string]string `json:"versions"`
	Warnings         []string          `json:"warnings,omitempty"`
	PreferencesSaved bool              `json:"preferences_saved,omitempty"`
}
type voiceOutcome struct {
	result   voice.Result
	prepared voicePreparation
	err      error
}

func runVoiceCommand(ctx context.Context, p invocation, in io.Reader, o *output) int {
	home, err := invocationHome(p)
	if err != nil {
		return o.problem(err)
	}
	var terminal *terminalUI
	if !o.json && !p.noInput && !p.set["plain"] && os.Getenv("TERM") != "dumb" && isTerminal(in) && isTerminal(o.out) {
		terminal = newTerminalUI(in, o.out, o.cancel, !p.set["no-color"] && os.Getenv("NO_COLOR") == "")
		o.terminal = terminal
		o.out, o.errOut = terminal.writer, terminal.writer
		location, _ := os.Getwd()
		if err := terminal.setChrome(terminalChrome{Version: Version, Location: location, Model: "Voice input", Context: "Transcript only | no task submitted"}); err != nil {
			return o.problem(err)
		}
		if err := terminal.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "Voice input · review before accepting · Ctrl+C cancel", Dim: true}}, CursorRow: -1}); err != nil {
			return o.problem(err)
		}
	}
	result, prepared, err := performVoice(ctx, p, home, terminal, o)
	if err == errVoiceDismissed {
		_ = o.text("Voice canceled. No task was submitted.\n")
		return 0
	}
	if err != nil {
		return o.problem(optionalProblem(err))
	}
	if !o.json {
		for _, warning := range prepared.Warnings {
			_ = o.progress("Voice setup warning: " + warning + "\n")
		}
	}
	if p.command == "voice setup" {
		if o.json {
			return optionalOutput(o, o.data("voice_ready", prepared))
		}
		_ = o.text("Voice setup remembered. Microphone was not opened.\nModel: " + prepared.Model + "\n")
		return 0
	}
	if o.json {
		if err := o.data("voice_prepared", prepared); err != nil {
			return o.problem(err)
		}
		return optionalOutput(o, o.data("voice", result))
	}
	_ = o.text(result.Text + "\n")
	return 0
}

func prepareVoice(ctx context.Context, p invocation, home string, status func(string)) (voice.Config, voicePreparation, error) {
	if len(p.language) > 16 || strings.ContainsRune(p.language, 0) || !utf8.ValidString(p.language) {
		return voice.Config{}, voicePreparation{}, usageError("voice language must be a UTF-8 code of at most 16 bytes without NUL")
	}
	model := p.selection.Model
	if model == "" || model == "auto" {
		model = "whisper/tiny.en"
	}
	if p.voiceModelDir == "" && (p.set["model"] || os.Getenv("UCLI_VOICE_MODEL_DIR") == "") && model == "whisper/tiny.en" && p.language != "" && p.language != "en" {
		return voice.Config{}, voicePreparation{}, usageError("whisper/tiny.en transcribes English; choose an existing multilingual --model-dir for another language")
	}
	modelDir := p.voiceModelDir
	if modelDir == "" && !p.set["model"] {
		modelDir = os.Getenv("UCLI_VOICE_MODEL_DIR")
	}
	if modelDir != "" {
		var err error
		modelDir, err = filepath.Abs(modelDir)
		if err != nil {
			return voice.Config{}, voicePreparation{}, err
		}
		// Bad explicit paths must fail before dependency/model downloads.
		if err := voice.ValidateModelDirectory(modelDir); err != nil {
			return voice.Config{}, voicePreparation{}, err
		}
		model = "local"
	} else {
		known := false
		for _, entry := range assets.Catalog() {
			known = known || entry.ID == model
		}
		if !known {
			return voice.Config{}, voicePreparation{}, usageError("unknown transcription model " + model + "; inspect ucli voice models")
		}
	}
	python := p.voicePython
	if python == "" {
		python = os.Getenv("UCLI_VOICE_PYTHON")
	}
	site := p.voiceSite
	if site == "" {
		site = os.Getenv("UCLI_VOICE_SITE")
	}
	status("Preparing voice runtime; reusing installed packages where available")
	backend, err := voiceenv.Ensure(ctx, home, voiceenv.Options{Python: python, SiteDir: site, Offline: p.offline, AllowInstall: !p.offline, Capture: p.command != "voice transcribe"})
	if err != nil {
		return voice.Config{}, voicePreparation{}, err
	}
	var warnings []string
	if modelDir == "" {
		status("Preparing requested transcription model; downloading only if missing")
		materialized, err := assets.PrepareModel(ctx, filepath.Join(home, "cache", "optional"), model, p.offline)
		if err != nil {
			return voice.Config{}, voicePreparation{}, err
		}
		modelDir = materialized.Directory
		warnings = append(warnings, materialized.Warnings...)
	}
	language := p.language
	if language == "" {
		language = "en"
	}
	cfg := voice.Config{Python: backend.Python, SiteDir: backend.SiteDir, ModelDir: modelDir, Language: language, MaxDuration: time.Duration(p.voiceDuration) * time.Second}
	if cfg.MaxDuration == 0 {
		cfg.MaxDuration = 30 * time.Second
	}
	if err := voice.Validate(cfg); err != nil {
		return voice.Config{}, voicePreparation{}, err
	}
	return cfg, voicePreparation{Python: backend.Python, SiteDir: backend.SiteDir, RuntimeSource: backend.Source, Model: model, ModelDir: modelDir, Versions: backend.Versions, Warnings: warnings}, nil
}

// Two fixed workers own the operation and its bounded status bridge. Every
// dismissal cancels and joins before another voice take can change the draft.
func performVoice(ctx context.Context, p invocation, home string, terminal *terminalUI, o *output) (voice.Result, voicePreparation, error) {
	var err error
	p, err = applyVoiceProfile(ctx, p, home)
	if err != nil {
		return voice.Result{}, voicePreparation{}, err
	}
	if p.command == "voice transcribe" {
		file, err := filepath.Abs(p.file)
		if err != nil {
			return voice.Result{}, voicePreparation{}, err
		}
		p.file = file
		if err := voice.ValidateAudio(file, time.Duration(p.voiceDuration)*time.Second); err != nil {
			return voice.Result{}, voicePreparation{}, err
		}
	}
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	wake := make(chan struct{}, 1)
	events := make(chan voice.Event, 16)
	bridgeDone := make(chan struct{})
	finished := make(chan voiceOutcome, 1)
	stop := make(chan struct{})
	var stopOnce sync.Once
	var mu sync.Mutex
	current := "Preparing voice"
	recording := false
	setStatus := func(value string) {
		mu.Lock()
		current = value
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go func() {
		defer close(bridgeDone)
		for event := range events {
			switch event.Type {
			case "loading":
				setStatus("Loading local transcription model")
			case "recording":
				mu.Lock()
				recording = true
				mu.Unlock()
				setStatus("Recording · " + event.Device)
			case "capture_stopped":
				mu.Lock()
				recording = false
				mu.Unlock()
				setStatus("Microphone stopped; preparing transcription")
			case "transcribing":
				setStatus("Transcribing locally")
			}
		}
	}()
	go func() {
		defer close(events)
		cfg, prepared, err := prepareVoice(jobCtx, p, home, setStatus)
		if err == nil && p.command == "voice setup" {
			err = saveVoiceProfile(jobCtx, home, p, prepared)
			prepared.PreferencesSaved = err == nil
		}
		result := voice.Result{}
		if err == nil && p.command != "voice setup" {
			if p.command == "voice transcribe" {
				setStatus("Loading local model and transcribing audio")
				result, err = voice.Transcribe(jobCtx, cfg, p.file)
			} else {
				result, err = voice.Capture(jobCtx, cfg, stop, events)
			}
		}
		finished <- voiceOutcome{result, prepared, err}
		select {
		case wake <- struct{}{}:
		default:
		}
	}()
	join := func(reason error) (voice.Result, voicePreparation, error) {
		cancel()
		select {
		case outcome := <-finished:
			select {
			case <-bridgeDone:
			case <-time.After(time.Second):
				return voice.Result{}, voicePreparation{}, core.Problem("voice_cleanup_timeout", "Voice status cleanup was not confirmed.", 9)
			}
			return voice.Result{}, voicePreparation{}, interruptedVoice(reason, outcome.err)
		case <-time.After(8 * time.Second):
			return voice.Result{}, voicePreparation{}, core.Problem("voice_cleanup_timeout", "Voice cleanup was not confirmed before its deadline; no transcript was accepted.", 9)
		}
	}
	lastStatus := ""
	if terminal == nil && !o.json && p.command == "voice record" {
		_ = o.progress(fmt.Sprintf("Timed recording: up to %d seconds; Ctrl+C cancels. Enter-stop is available in the rich terminal.\n", p.voiceDuration))
	}
	for {
		select {
		case outcome := <-finished:
			<-bridgeDone
			if err := ctx.Err(); err != nil {
				return voice.Result{}, voicePreparation{}, interruptedVoice(err, outcome.err)
			}
			return outcome.result, outcome.prepared, outcome.err
		default:
		}
		mu.Lock()
		state, isRecording := current, recording
		mu.Unlock()
		if terminal == nil {
			if state != lastStatus {
				if o.json {
					_ = o.data("voice_state", map[string]string{"state": state})
				} else {
					_ = o.progress(state + "\n")
				}
				lastStatus = state
			}
			select {
			case <-wake:
				continue
			case <-ctx.Done():
				return join(ctx.Err())
			}
		}
		width, height := terminal.size()
		hint := "Esc cancel · no AI task is sent"
		if isRecording {
			hint = "Enter stop · Esc cancel · no AI task is sent"
		}
		detail := "Local WAV transcription · microphone stays closed"
		if p.command == "voice record" {
			detail = fmt.Sprintf("Up to %d seconds · local transcription", p.voiceDuration)
		}
		if p.command == "voice setup" {
			detail = "Preparing voice · microphone stays closed"
		}
		lines := []terminalLine{{Text: "Voice · draft input", Dim: true}, {Text: state}, {Text: detail, Dim: true}, {Text: hint, Dim: true}}
		if height < 6 {
			lines = []terminalLine{{Text: state}, {Text: hint, Dim: true}}
		}
		if width < 16 || height < 4 {
			lines = []terminalLine{{Text: "Voice active · Esc cancel"}}
		}
		if err := terminal.paint(terminalFrame{Lines: lines, CursorRow: -1}); err != nil {
			return join(err)
		}
		event, err := terminal.nextOr(ctx, wake)
		if err != nil {
			return join(err)
		}
		switch event.kind {
		case keyEscape, keyEOF:
			return join(errVoiceDismissed)
		case keyEnter:
			if isRecording {
				stopOnce.Do(func() { close(stop) })
				mu.Lock()
				recording = false
				mu.Unlock()
				setStatus("Stopping recording; waiting for microphone acknowledgement")
			}
		}
	}
}

func (s *session) voiceText(ctx context.Context, file string) (string, error) {
	if s.recoveryID != "" {
		return "", core.Problem("draft_conflict", "Choose /tasks or /new before adding voice text.", 5)
	}
	p := invocation{command: "voice record", voiceDuration: 30, set: map[string]bool{}}
	if strings.TrimSpace(file) != "" {
		p.command = "voice transcribe"
		p.file = file
		if !filepath.IsAbs(file) {
			p.file = filepath.Join(s.cwd, file)
		}
	}
	result, prepared, err := performVoice(ctx, p, s.app.Home, s.terminal, s.out)
	if err == errVoiceDismissed {
		_ = s.out.text("Voice canceled. Your draft is kept.\n")
		return "", nil
	}
	if err != nil {
		return "", optionalProblem(err)
	}
	for _, warning := range prepared.Warnings {
		_ = s.out.progress("Voice setup warning: " + warning + "\n")
	}
	return result.Text, nil
}

func insertVoiceText(editor *textEditor, text string) error {
	if text == "" {
		return nil
	}
	if editor.cursor > 0 && editor.text[editor.cursor-1] != '\n' {
		text = "\n" + text
	}
	if editor.cursor < len(editor.text) && editor.text[editor.cursor] != '\n' {
		text += "\n"
	}
	return editor.apply(keyEvent{kind: keyPaste, text: text})
}
