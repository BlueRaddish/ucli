package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "-I" {
		fixture()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixture() {
	var req request
	if json.NewDecoder(os.Stdin).Decode(&req) != nil {
		os.Exit(2)
	}
	mode, _ := os.ReadFile(filepath.Join(req.ModelDir, "config.json"))
	_ = os.WriteFile(filepath.Join(req.ModelDir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
	emit := func(kind string, extra map[string]any) {
		value := map[string]any{"version": 1, "type": kind, "generation": req.Generation}
		for k, v := range extra {
			value[k] = v
		}
		_ = json.NewEncoder(os.Stdout).Encode(value)
	}
	emit("loading", nil)
	switch string(mode) {
	case "failure":
		emit("error", map[string]any{"code": "dependencies_missing"})
		return
	case "hang":
		time.Sleep(time.Minute)
		return
	case "malformed":
		fmt.Print("{\"version\":1")
		return
	case "oversize":
		fmt.Print(strings.Repeat("x", maxFrame+1))
		return
	case "duplicate":
		fmt.Printf("{\"version\":1,\"version\":1,\"type\":\"transcribing\",\"generation\":%q}\n", req.Generation)
		return
	}
	if req.Mode == "capture" {
		emit("recording", map[string]any{"device": "Synthetic input"})
		var command map[string]any
		if json.NewDecoder(os.Stdin).Decode(&command) != nil || command["generation"] != req.Generation || command["type"] != "stop" {
			os.Exit(4)
		}
		if string(mode) == "no-ack" {
			time.Sleep(time.Minute)
			return
		}
		emit("capture_stopped", nil)
	}
	emit("transcribing", nil)
	if string(mode) == "stale" {
		req.Generation = "00000000000000000000000000000000"
	}
	emit("result", map[string]any{"text": "/send literal transcript 世界", "language": "en", "duration_seconds": .1, "versions": map[string]string{"fixture": "1"}})
	if string(mode) == "exit" {
		os.Exit(7)
	}
	if string(mode) == "late" {
		_ = os.WriteFile(filepath.Join(req.ModelDir, "result-written"), []byte("ready"), 0600)
		time.Sleep(time.Minute)
	}
}

func fixtureConfig(t *testing.T, mode string) (Config, string) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("owned external process launch is explicitly unsupported on macOS")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"config.json", "model.bin", "tokenizer.json", "vocabulary.txt"} {
		data := "fixture"
		if name == "config.json" {
			data = mode
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	wav := filepath.Join(t.TempDir(), "synthetic.wav")
	writeWave(t, wav, 16000, 1600)
	return Config{Python: exe, ModelDir: dir, MaxDuration: time.Second}, wav
}
func writeWave(t *testing.T, path string, rate, frames int) {
	t.Helper()
	data := make([]byte, 44+frames*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(frames*2))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func requireStopped(t *testing.T, cfg Config) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cfg.ModelDir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(raw))
	alive, err := process.Alive(pid)
	if err != nil || alive {
		t.Fatalf("owned helper still alive: %d %v", pid, err)
	}
}

func TestVoiceFileReturnsLiteralDataAfterSuccessfulExit(t *testing.T) {
	cfg, wav := fixtureConfig(t, "success")
	result, err := Transcribe(context.Background(), cfg, wav)
	if err != nil || result.Text != "/send literal transcript 世界" || len(result.Generation) != 32 || result.DurationSeconds != .1 || result.Versions["fixture"] != "1" {
		t.Fatalf("transcription: %+v %v", result, err)
	}
	requireStopped(t, cfg)
}
func TestVoiceRejectsMalformedStaleAndUnsettledOutput(t *testing.T) {
	for _, mode := range []string{"failure", "malformed", "oversize", "duplicate", "stale", "exit"} {
		t.Run(mode, func(t *testing.T) {
			cfg, wav := fixtureConfig(t, mode)
			result, err := Transcribe(context.Background(), cfg, wav)
			if err == nil || result.Text != "" {
				t.Fatalf("invalid helper accepted: %+v %v", result, err)
			}
			requireStopped(t, cfg)
		})
	}
}
func TestVoiceCancellationOwnsChildAndDiscardsLateResult(t *testing.T) {
	for _, mode := range []string{"hang", "late"} {
		t.Run(mode, func(t *testing.T) {
			cfg, wav := fixtureConfig(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type outcome struct {
				result Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() { result, err := Transcribe(ctx, cfg, wav); done <- outcome{result, err} }()
			marker := "pid"
			if mode == "late" {
				marker = "result-written"
			}
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(10 * time.Millisecond)
			defer poll.Stop()
			ready := false
			for !ready {
				if _, err := os.Stat(filepath.Join(cfg.ModelDir, marker)); err == nil {
					ready = true
					break
				}
				select {
				case early := <-done:
					t.Fatalf("fixture ended before readiness: %+v", early)
				case <-deadline.C:
					cancel()
					<-done
					t.Fatal("fixture did not become ready")
				case <-poll.C:
				}
			}
			cancel()
			var stopped outcome
			select {
			case stopped = <-done:
			case <-time.After(7 * time.Second):
				t.Fatal("owned cancellation did not return")
			}
			if !errors.Is(stopped.err, context.Canceled) || stopped.result.Text != "" {
				t.Fatalf("cancellation lost: %+v %v", stopped.result, stopped.err)
			}
			var problem *core.Error
			if errors.As(stopped.err, &problem) && problem.Exit == 9 {
				t.Fatalf("confirmed cancellation was treated as cleanup uncertainty: %v", stopped.err)
			}
			requireStopped(t, cfg)
		})
	}
}

func TestCleanupFailurePrecedesRecoverableError(t *testing.T) {
	previous := problem("voice_dependencies_missing", "Fixture prerequisite failure")
	failure := errors.New("fixture owned-process cleanup failed")
	for _, confirmed := range []bool{false, true} {
		got := cleanupError(previous, errors.Join(context.Canceled, failure), confirmed)
		var typed *core.Error
		if !errors.As(got, &typed) || typed.Exit != 9 || !errors.Is(got, previous) || !errors.Is(got, failure) {
			t.Fatalf("cleanup failure hidden (confirmed=%v): %v", confirmed, got)
		}
	}
	for _, canceled := range []error{context.Canceled, context.DeadlineExceeded, fmt.Errorf("wrapped: %w", errors.Join(context.Canceled, context.DeadlineExceeded))} {
		got := cleanupError(nil, canceled, true)
		var typed *core.Error
		if errors.As(got, &typed) || !errors.Is(got, canceled) {
			t.Fatalf("confirmed context-only cleanup escalated: %v", got)
		}
		got = cleanupError(nil, canceled, false)
		if !errors.As(got, &typed) || typed.Exit != 9 {
			t.Fatalf("unconfirmed timeout claimed clean: %v", got)
		}
	}
}

func TestModelPreflightRejectsOversizeBeforeInterpreterExists(t *testing.T) {
	cfg, _ := fixtureConfig(t, "success")
	if err := ValidateModelDirectory(cfg.ModelDir); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(cfg.ModelDir, "config.json"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate((1 << 20) + 1)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("fixture bounds: %v %v", err, closeErr)
	}
	if err := ValidateModelDirectory(cfg.ModelDir); err == nil {
		t.Fatal("oversized explicit model accepted before runtime acquisition")
	}
	if _, err := os.Stat(filepath.Join(cfg.ModelDir, "pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("metadata validation launched interpreter")
	}
}
func TestVoiceCaptureExplicitStopDurationAndFeedback(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			cfg, _ := fixtureConfig(t, "success")
			stop := make(chan struct{})
			events := make(chan Event, 8)
			defer close(events)
			if !automatic {
				go func() {
					for event := range events {
						if event.Type == "recording" {
							close(stop)
							return
						}
					}
				}()
			}
			start := time.Now()
			result, err := Capture(context.Background(), cfg, stop, events)
			if err != nil || result.Text == "" {
				t.Fatalf("capture: %+v %v", result, err)
			}
			if automatic && time.Since(start) < time.Second {
				t.Fatal("Go duration ceiling did not trigger the stop")
			}
			requireStopped(t, cfg)
		})
	}
	cfg, _ := fixtureConfig(t, "success")
	closed := make(chan struct{})
	close(closed)
	if _, err := Capture(context.Background(), cfg, closed, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("already stopped capture opened helper")
	}
	if _, err := os.Stat(filepath.Join(cfg.ModelDir, "pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("already stopped capture launched")
	}
}
func TestVoiceCaptureMissingStopAckAndBlockedFeedbackFailClosed(t *testing.T) {
	cfg, _ := fixtureConfig(t, "no-ack")
	stop := make(chan struct{})
	events := make(chan Event, 8)
	defer close(events)
	go func() {
		for event := range events {
			if event.Type == "recording" {
				close(stop)
				return
			}
		}
	}()
	result, err := Capture(context.Background(), cfg, stop, events)
	var problem *core.Error
	if result.Text != "" || !errors.As(err, &problem) || problem.Code != "voice_stop_timeout" {
		t.Fatalf("unacknowledged microphone stop: %+v %v", result, err)
	}
	requireStopped(t, cfg)
	cfg, _ = fixtureConfig(t, "success")
	if result, err = Capture(context.Background(), cfg, make(chan struct{}), make(chan Event)); err == nil || result.Text != "" {
		t.Fatal("blocked feedback did not stop capture")
	}
	requireStopped(t, cfg)
}
func TestVoiceValidatesOnlyExplicitLocalInputs(t *testing.T) {
	cfg, wav := fixtureConfig(t, "success")
	for _, mutate := range []func(*Config){func(c *Config) { c.Python = "python" }, func(c *Config) { c.ModelDir = "base.en" }, func(c *Config) { c.MaxDuration = 121 * time.Second }, func(c *Config) { c.SiteDir = "relative" }} {
		bad := cfg
		mutate(&bad)
		if _, err := Transcribe(context.Background(), bad, wav); err == nil {
			t.Fatal("invalid config launched")
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.ModelDir, "pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("validation launched a process")
	}
	if err := os.Remove(filepath.Join(cfg.ModelDir, "tokenizer.json")); err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("missing tokenizer could trigger an implicit lookup")
	}
}

func TestVoiceStopDuringLoadCancelsWithoutWaitingForModel(t *testing.T) {
	cfg, _ := fixtureConfig(t, "hang")
	stop, events := make(chan struct{}), make(chan Event, 8)
	defer close(events)
	go func() {
		for event := range events {
			if event.Type == "loading" {
				close(stop)
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := Capture(ctx, cfg, stop, events)
	if !errors.Is(err, context.Canceled) || result.Text != "" {
		t.Fatalf("stop during model load: %+v %v", result, err)
	}
	requireStopped(t, cfg)
}
