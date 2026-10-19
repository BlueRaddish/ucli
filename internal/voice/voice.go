// Package voice runs an explicitly requested, local-only transcription helper.
// It returns text, never task execution or a clipboard/global keyboard action.
package voice

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

const MaxDuration = 120 * time.Second
const maxFrame = 64 * 1024

//go:embed helper.py
var helper string

type Config struct {
	Python, ModelDir, Language, Device string
	SiteDir                            string        // Explicit external site; its .pth files are not executed. Interpreter startup remains trusted.
	MaxDuration                        time.Duration // Zero chooses 120 seconds; otherwise 1..120 seconds.
}
type Event struct {
	Version    int    `json:"version"`
	Type       string `json:"type"`
	Generation string `json:"generation"`
	Device     string `json:"device,omitempty"`
}
type Result struct {
	Text            string            `json:"text"`
	Language        string            `json:"language"`
	Generation      string            `json:"generation"`
	DurationSeconds float64           `json:"duration_seconds"`
	Versions        map[string]string `json:"versions"`
	SiteDir         string            `json:"site_dir,omitempty"`
}
type request struct {
	Version    int     `json:"version"`
	Generation string  `json:"generation"`
	Mode       string  `json:"mode"`
	ModelDir   string  `json:"model_dir"`
	AudioPath  string  `json:"audio_path,omitempty"`
	Language   string  `json:"language,omitempty"`
	Device     string  `json:"device,omitempty"`
	MaxSeconds float64 `json:"max_seconds"`
}
type wireEvent struct {
	Event
	Text            string            `json:"text,omitempty"`
	Language        string            `json:"language,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	Versions        map[string]string `json:"versions,omitempty"`
	Code            string            `json:"code,omitempty"`
	SiteDir         string            `json:"site_dir,omitempty"`
}

func problem(code, message string) error { return core.Problem(code, message, 3) }

// Validate is local metadata validation only: no interpreter/model load or network.
// The artifact installer separately owns hashes, provenance and redistribution.
func Validate(cfg Config) error {
	if cfg.MaxDuration != 0 && (cfg.MaxDuration < time.Second || cfg.MaxDuration > MaxDuration) {
		return problem("voice_config", "Recording duration must be between 1 and 120 seconds.")
	}
	if !filepath.IsAbs(cfg.Python) || !filepath.IsAbs(cfg.ModelDir) || strings.ContainsRune(cfg.Python+cfg.ModelDir+cfg.Language+cfg.Device, 0) {
		return problem("voice_config", "Voice needs absolute Python and local model paths without NUL.")
	}
	if len(cfg.Language) > 16 || len(cfg.Device) > 256 || !utf8.ValidString(cfg.Language+cfg.Device) {
		return problem("voice_config", "Voice language or device is invalid.")
	}
	if cfg.SiteDir != "" {
		info, err := os.Lstat(cfg.SiteDir)
		if !filepath.IsAbs(cfg.SiteDir) || strings.ContainsRune(cfg.SiteDir, 0) || err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return problem("voice_config", "External Python site must be an explicitly selected absolute directory.")
		}
	}
	if info, err := os.Stat(cfg.Python); err != nil || !info.Mode().IsRegular() {
		return problem("voice_python_missing", "Configured Python interpreter is unavailable. Complete voice setup first.")
	}
	return ValidateModelDirectory(cfg.ModelDir)
}

// ValidateModelDirectory checks required local files before runtime acquisition.
// It does not load model contents or assert their provenance.
func ValidateModelDirectory(modelDir string) error {
	if !filepath.IsAbs(modelDir) || strings.ContainsRune(modelDir, 0) {
		return problem("voice_model_missing", "Choose an absolute materialized local transcription model directory.")
	}
	if info, err := os.Lstat(modelDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return problem("voice_model_missing", "Choose a materialized local transcription model directory.")
	}
	for name, limit := range map[string]int64{"config.json": 1 << 20, "model.bin": 2 << 30, "tokenizer.json": 16 << 20, "vocabulary.txt": 4 << 20} {
		info, err := os.Lstat(filepath.Join(modelDir, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
			return problem("voice_model_missing", "Local model files are missing, linked, empty or outside their bounds. Repair voice setup.")
		}
	}
	return nil
}

func Transcribe(ctx context.Context, cfg Config, audioPath string) (Result, error) {
	if err := ValidateAudio(audioPath, cfg.MaxDuration); err != nil {
		return Result{}, err
	}
	return run(ctx, cfg, "file", audioPath, nil, nil)
}

// Capture opens a microphone only for this call. Closing stop finishes the take;
// canceling ctx discards it and stops the owned process. events is caller-owned,
// never closed here; a full channel fails instead of blocking capture teardown.
func Capture(ctx context.Context, cfg Config, stop <-chan struct{}, events chan<- Event) (Result, error) {
	if stop == nil {
		return Result{}, problem("voice_config", "Microphone capture needs an explicit stop control.")
	}
	select {
	case <-stop:
		return Result{}, context.Canceled
	default:
	}
	return run(ctx, cfg, "capture", "", stop, events)
}

func run(ctx context.Context, cfg Config, mode, path string, stop <-chan struct{}, events chan<- Event) (result Result, err error) {
	if ctx == nil {
		return result, problem("voice_config", "Voice requires a lifetime context.")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = Validate(cfg); err != nil {
		return result, err
	}
	if cfg.MaxDuration == 0 {
		cfg.MaxDuration = MaxDuration
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return result, err
	}
	generation := hex.EncodeToString(nonce[:])
	work, err := os.MkdirTemp("", "ucli-voice-")
	if err != nil {
		return result, problem("voice_start", "Cannot create private voice runtime directory.")
	}
	defer func() {
		if cleanErr := os.RemoveAll(work); cleanErr != nil {
			err = cleanupError(err, cleanErr, false)
			result = Result{}
		}
	}()
	lifetime, cancel := context.WithTimeout(ctx, cfg.MaxDuration+120*time.Second)
	defer cancel()
	env := []string{"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "HF_HOME=" + work, "HOME=" + work, "USERPROFILE=" + work, "TMP=" + work, "TEMP=" + work, "KMP_BLOCKTIME=0"}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	args := []string{"-I", "-B", "-u", "-c", helper}
	if cfg.SiteDir != "" {
		args = append(args, "--site-dir", cfg.SiteDir)
	}
	child, err := process.Start(lifetime, process.Spec{Executable: cfg.Python, Args: args, Env: env, Dir: work, FrameBytes: maxFrame, QueueBytes: 4 * maxFrame})
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := child.Close(); closeErr != nil {
			err = cleanupError(err, closeErr, child.Settled())
			result = Result{}
		}
	}()
	payload, _ := json.Marshal(request{1, generation, mode, cfg.ModelDir, path, cfg.Language, cfg.Device, cfg.MaxDuration.Seconds()})
	if err = child.Send(lifetime, append(payload, '\n')); err != nil {
		return result, err
	}
	var buffer []byte
	var captureTimer, ackTimer *time.Timer
	var captureDeadline, ackDeadline <-chan time.Time
	defer func() {
		if captureTimer != nil {
			captureTimer.Stop()
		}
		if ackTimer != nil {
			ackTimer.Stop()
		}
	}()
	state := "starting"
	finished := false
	total := 0
	sendStop := func() error {
		if state != "recording" {
			// Stopping during model load cancels the take before a microphone can
			// be intentionally opened; do not wait for a slow load to finish.
			return context.Canceled
		}
		stop = nil
		captureDeadline = nil
		command, _ := json.Marshal(map[string]any{"version": 1, "generation": generation, "type": "stop"})
		if sendErr := child.Send(lifetime, append(command, '\n')); sendErr != nil {
			return sendErr
		}
		if state == "recording" {
			ackTimer = time.NewTimer(2 * time.Second)
			ackDeadline = ackTimer.C
		}
		return nil
	}
	for {
		select {
		case <-lifetime.Done():
			return Result{}, lifetime.Err()
		case <-stop:
			if err = sendStop(); err != nil {
				return Result{}, err
			}
		case <-captureDeadline:
			if err = sendStop(); err != nil {
				return Result{}, err
			}
		case <-ackDeadline:
			return Result{}, problem("voice_stop_timeout", "Microphone stop was not acknowledged; stopping the owned helper.")
		case chunk, ok := <-child.Events():
			if !ok {
				exit, waitErr := child.Wait(lifetime)
				if waitErr != nil {
					return Result{}, waitErr
				}
				if exit.Code != 0 || !finished || len(buffer) > 0 {
					return Result{}, problem("voice_protocol", "Voice helper ended without a complete matching result.")
				}
				if err = ctx.Err(); err != nil {
					return Result{}, err
				}
				return result, nil
			}
			total += len(chunk.Data)
			if total > 4*maxFrame {
				return Result{}, problem("voice_protocol", "Voice helper exceeded its output bound.")
			}
			if chunk.Stream != "stdout" {
				continue
			} // Native diagnostics are never copied into receipts.
			buffer = append(buffer, chunk.Data...)
			for {
				index := bytes.IndexByte(buffer, '\n')
				if index < 0 {
					break
				}
				line := buffer[:index]
				buffer = buffer[index+1:]
				if len(line) > maxFrame || !utf8.Valid(line) || !validObject(line, true) {
					return Result{}, problem("voice_protocol", "Voice helper sent an invalid frame.")
				}
				var event wireEvent
				decoder := json.NewDecoder(bytes.NewReader(line))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&event) != nil || decoder.Decode(new(any)) != io.EOF || event.Version != 1 || event.Generation != generation || len(event.Device) > 256 || strings.ContainsRune(event.Device, 0) || finished {
					return Result{}, problem("voice_protocol", "Voice helper response did not match this take.")
				}
				switch event.Type {
				case "loading":
					if state != "starting" {
						return Result{}, problem("voice_protocol", "Unexpected voice state.")
					}
					state = "loading"
				case "recording":
					if mode != "capture" || state != "loading" {
						return Result{}, problem("voice_protocol", "Unexpected microphone state.")
					}
					state = "recording"
					captureTimer = time.NewTimer(cfg.MaxDuration)
					captureDeadline = captureTimer.C
				case "capture_stopped":
					if state != "recording" {
						return Result{}, problem("voice_protocol", "Unexpected microphone stop.")
					}
					state = "stopped"
					stop = nil
					captureDeadline = nil
					ackDeadline = nil
				case "transcribing":
					if state != "loading" && state != "stopped" || mode == "capture" && state != "stopped" {
						return Result{}, problem("voice_protocol", "Unexpected transcription state.")
					}
					state = "transcribing"
				case "result":
					if state != "transcribing" || !utf8.ValidString(event.Text) || strings.ContainsRune(event.Text, 0) || len(event.Text) > 32*1024 || math.IsNaN(event.DurationSeconds) || math.IsInf(event.DurationSeconds, 0) || event.DurationSeconds < 0 || event.DurationSeconds > cfg.MaxDuration.Seconds()+.01 || len(event.Language) > 16 || len(event.Versions) == 0 {
						return Result{}, problem("voice_protocol", "Voice result is invalid or outside its bounds.")
					}
					if event.SiteDir != cfg.SiteDir {
						return Result{}, problem("voice_protocol", "Voice dependency provenance did not match the selected site.")
					}
					if len(event.Versions) > 10 {
						return Result{}, problem("voice_protocol", "Voice version receipt exceeded its bound.")
					}
					for key, value := range event.Versions {
						if len(key) > 32 || len(value) > 64 || strings.ContainsRune(key+value, 0) {
							return Result{}, problem("voice_protocol", "Voice version receipt is invalid.")
						}
					}
					result = Result{event.Text, event.Language, generation, event.DurationSeconds, event.Versions, event.SiteDir}
					finished = true
				case "canceled":
					return Result{}, context.Canceled
				case "error":
					return Result{}, helperError(event.Code)
				default:
					return Result{}, problem("voice_protocol", "Voice helper sent an unknown event.")
				}
				if events != nil && !finished {
					select {
					case events <- event.Event:
					default:
						return Result{}, problem("voice_events_blocked", "Voice feedback was not consumed; capture stopped.")
					}
				}
			}
			if len(buffer) > maxFrame {
				return Result{}, problem("voice_protocol", "Voice helper frame exceeded its bound.")
			}
		}
	}
}

func cleanupError(previous, cleanup error, confirmed bool) error {
	if cleanup == nil {
		return previous
	}
	if confirmed && contextOnly(cleanup) {
		return errors.Join(previous, cleanup)
	}
	// Put the stronger outcome first: errors.As must not return an earlier
	// recoverable prerequisite error while teardown remains uncertain.
	return errors.Join(core.Problem("voice_cleanup_unconfirmed", "Voice cleanup failed or could not be confirmed; no transcript was accepted.", 9), previous, cleanup)
}

func contextOnly(err error) bool {
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		children := many.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !contextOnly(child) {
				return false
			}
		}
		return true
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return contextOnly(one.Unwrap())
	}
	return false
}

func validObject(raw []byte, top bool) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	allowed := map[string]bool{"version": true, "type": true, "generation": true, "device": true, "text": true, "language": true, "duration_seconds": true, "versions": true, "code": true, "site_dir": true}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[strings.ToLower(key)] || top && !allowed[key] {
			return false
		}
		seen[strings.ToLower(key)] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
		if top && key == "versions" && !validObject(value, false) {
			return false
		}
	}
	_, err := decoder.Token()
	return err == nil && decoder.Decode(new(any)) == io.EOF
}

func helperError(code string) error {
	messages := map[string]string{"dependencies_missing": "Voice dependencies are missing from the isolated interpreter. Complete voice setup.", "dependencies_incompatible": "Voice dependency versions are unsupported.", "audio_invalid": "Use a 16 kHz mono PCM16 WAV within the recording duration limit.", "capture_unavailable": "Microphone capture is unavailable or the selected input cannot provide 16 kHz mono audio.", "capture_overflow": "Microphone audio overflowed; retry a shorter take.", "model_invalid": "The local model could not load; check voice setup.", "transcription_failed": "Local transcription failed; the draft is unchanged.", "request_invalid": "Voice helper rejected its request."}
	if message, ok := messages[code]; ok {
		return problem("voice_"+code, message)
	}
	return problem("voice_helper_failed", "Local voice helper failed; the draft is unchanged.")
}
