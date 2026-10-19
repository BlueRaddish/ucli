package voice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

const fakeNumpy = `float32 = "float32"
class Array:
    def __init__(self, size): self.size = size
    def __len__(self): return self.size
    def astype(self, dtype): return self
    def __truediv__(self, divisor): return self
    def __getitem__(self, key):
        if isinstance(key, tuple): key = key[0]
        return Array(len(range(self.size)[key]))
    def copy(self): return Array(self.size)
def frombuffer(data, dtype):
    assert dtype == "<i2"
    return Array(len(data)//2)
def concatenate(parts): return Array(sum(len(p) for p in parts))
def zeros(size, dtype=None): return Array(size)
`
const fakeWhisper = `import os, pathlib, types
class WhisperModel:
    def __init__(self, model, **kwargs):
        assert pathlib.Path(model).is_absolute()
        assert kwargs == dict(device="cpu",compute_type="int8",cpu_threads=4,local_files_only=True)
        assert os.environ["HF_HUB_OFFLINE"] == "1"
        assert os.environ["TRANSFORMERS_OFFLINE"] == "1"
        assert "PYTHONPATH" not in os.environ
        print("fixture library log must not appear as protocol")
    def transcribe(self, audio, **kwargs):
        assert len(audio) in (1600,16000)
        assert kwargs["temperature"] == 0.0 and kwargs["condition_on_previous_text"] is False
        return iter([types.SimpleNamespace(text=" /send remains literal")]), types.SimpleNamespace(language="en")
`
const fakeSoundDevice = `from numpy import Array
class CallbackStop(Exception): pass
def query_devices(device=None, kind=None):
    record = {"max_input_channels":1,"name":"Synthetic microphone (no hardware)"}
    return record if kind else [record]
class InputStream:
    def __init__(self, **kwargs):
        assert kwargs["samplerate"] == 16000 and kwargs["channels"] == 1 and kwargs["dtype"] == "float32"
        self.callback = kwargs["callback"]
    def __enter__(self):
        try: self.callback(Array(1600),1600,None,False)
        except CallbackStop: pass
        return self
    def __exit__(self, *args): return False
`

func embeddedFixture(t *testing.T) (Config, string, string) {
	t.Helper()
	python := os.Getenv("UCLI_TEST_VOICE_PYTHON")
	if python == "" {
		t.Skip("set UCLI_TEST_VOICE_PYTHON to exercise embedded helper with fake packages; no microphone/network")
	}
	cfg, wav := fixtureConfig(t, "synthetic helper model")
	cfg.Python = python
	cfg.SiteDir = t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfg.SiteDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("numpy.py", fakeNumpy)
	write("faster_whisper.py", fakeWhisper)
	write("sounddevice.py", fakeSoundDevice)
	for name, version := range map[string]string{"numpy": "2.4.5", "faster-whisper": "1.2.1", "ctranslate2": "4.8.1", "tokenizers": "0.23.1", "huggingface-hub": "1.28.0", "sounddevice": "0.5.6"} {
		dir := filepath.Join(cfg.SiteDir, strings.ReplaceAll(name, "-", "_")+"-"+version+".dist-info")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "METADATA"), []byte("Metadata-Version: 2.1\nName: "+name+"\nVersion: "+version+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(cfg.SiteDir, "pth-executed")
	write("untrusted.pth", "import pathlib; pathlib.Path('"+strings.ReplaceAll(marker, "\\", "/")+"').write_text('executed')\n")
	write("sitecustomize.py", "raise RuntimeError('ambient sitecustomize must not execute')\n")
	return cfg, wav, marker
}

func TestEmbeddedHelperPCMOfflineAndExplicitSiteProvenance(t *testing.T) {
	cfg, wav, marker := embeddedFixture(t)
	result, err := Transcribe(context.Background(), cfg, wav)
	if err != nil || result.Text != "/send remains literal" || result.SiteDir != cfg.SiteDir || result.Versions["faster-whisper"] != "1.2.1" || result.DurationSeconds != .1 {
		t.Fatalf("embedded helper: %+v %v", result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("explicit site path executed .pth")
	}
	if _, err := os.Stat(filepath.Join(cfg.SiteDir, "__pycache__")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("voice changed the explicitly reused package directory")
	}
}

func TestEmbeddedHelperSyntheticCaptureStopsWithoutHardware(t *testing.T) {
	cfg, _, _ := embeddedFixture(t)
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
	if err != nil || result.Text != "/send remains literal" || result.Versions["sounddevice"] != "0.5.6" {
		t.Fatalf("synthetic capture: %+v %v", result, err)
	}
}

func TestEmbeddedHelperRejectsWrongPCMBeforeInference(t *testing.T) {
	cfg, wav, _ := embeddedFixture(t)
	writeWave(t, wav, 44100, 1600)
	result, err := Transcribe(context.Background(), cfg, wav)
	var problem *core.Error
	if result.Text != "" || !errors.As(err, &problem) || problem.Code != "voice_audio" {
		t.Fatalf("wrong format: %+v %v", result, err)
	}
}

func TestEmbeddedHelperOwnDurationClosesWithoutStopMessage(t *testing.T) {
	cfg, _, _ := embeddedFixture(t)
	// Fill the helper's sample ceiling immediately, before Go sends its timed
	// stop. The control reader is still waiting when Python shuts down.
	if err := os.WriteFile(filepath.Join(cfg.SiteDir, "sounddevice.py"), []byte(strings.ReplaceAll(fakeSoundDevice, "Array(1600),1600", "Array(16000),16000")), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Capture(context.Background(), cfg, make(chan struct{}), make(chan Event, 8))
	if err != nil || result.DurationSeconds != 1 || result.Text == "" {
		t.Fatalf("helper duration ceiling: %+v %v", result, err)
	}
}
