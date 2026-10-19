# Local voice input foundation

This package turns one explicitly requested PCM file or microphone take into
literal transcript data. It does not send a task, change a draft, start Murmur,
install anything, or run during help, model browsing or ordinary startup. The
caller owns lazy setup, selected ASR model, composer insertion and draft CAS.

```go
// Run this BEFORE lazy Python/model acquisition for file input.
err := voice.ValidateAudio(absoluteWAVPath, 30*time.Second)

cfg := voice.Config{
    Python: absolutePythonExecutable, ModelDir: absoluteMaterializedModelDir,
    SiteDir: explicitlySelectedExistingSiteDir, // optional
    MaxDuration: 30*time.Second, Language: "en",
}
result, err := voice.Transcribe(ctx, cfg, absoluteWAVPath)
// result.Text is data, including text such as /send. Never interpret it as a command.

// Only an explicit microphone action calls Capture. Closing stop finishes a take;
// canceling ctx discards it. Stop during model loading cancels before recording.
stop := make(chan struct{})
events := make(chan voice.Event, 8)
result, err = voice.Capture(ctx, cfg, stop, events)
```

The caller must consume `events` concurrently (or pass nil). It owns that channel;
Capture never closes it. A full channel fails and tears down capture rather than
blocking microphone cleanup. Events are `loading`, `recording` (with actual device
name), `capture_stopped`, and `transcribing`. A returned result is accepted only
after a matching generation, valid protocol and successful child exit. Errors
return an empty result. A typed error may be joined with cleanup failure; neither
a timeout nor a UI state alone proves cleanup succeeded.

## Local runtime and model boundary

`ValidateModelDirectory` checks all four local file bounds before dependency
acquisition; `Validate` reuses it and performs the remaining metadata checks
without launching Python. Python must be an
explicit absolute regular executable. The helper runs through `internal/process`
with `-I -B`, an explicit environment and private temporary cwd. `-B` suppresses
bytecode writes into reused packages. No caller cwd,
PYTHONPATH, native credentials or repository settings are inherited. HF offline
flags are set by both host and helper; `WhisperModel` receives the local directory
and `local_files_only=True`. CPU/int8 is the current deliberate baseline.

Optional `SiteDir` must be an absolute existing directory. It is added using
`sys.path.insert`, so its `.pth` and `sitecustomize.py` files are not executed as
startup hooks. **The selected interpreter's own startup and packages are trusted
code:** `-I` does not suppress interpreter-global startup hooks. This is explicit
runtime selection, not a Python sandbox or package admission. `Result.SiteDir`
and `Result.Versions` retain this dependency provenance. No site is discovered or
added automatically by this package.

The local model must contain these nonempty regular files:

| File | Maximum bytes |
| --- | ---: |
| `config.json` | 1 MiB |
| `model.bin` | 2 GiB |
| `tokenizer.json` | 16 MiB |
| `vocabulary.txt` | 4 MiB |

Model loading validates their actual contents. Artifact setup separately owns
hashes, immutable revisions, licenses and atomic publication. Model aliases or
cache fallbacks are not accepted here; a missing tokenizer cannot trigger lookup.

The helper checks Python >=3.9, faster-whisper 1.x and CTranslate2 4.x, and records
versions for Python, faster-whisper, CTranslate2, numpy, tokenizers and
huggingface-hub. Microphone mode additionally checks/imports sounddevice. These
major-version guards are not blanket compatibility certification. Setup must
resolve the packages' complete dependencies; faster-whisper may import PyAV and
ONNX Runtime transitively. File decoding uses stdlib `wave` and numpy, without
calling the PyAV decoder or requiring FFmpeg. Nothing runs `pip` here.

## Bounds, interruption and platforms

- Only 16 kHz, mono, signed PCM16 RIFF WAV files are accepted: at most 8 MiB,
  nonempty audio, at most 1,024 RIFF chunks, and within the configured duration.
  Preflight reads bounded headers; the helper checks format again before loading.
  No MP3, resampling, device-rate fallback or codec download is attempted.
- Maximum duration is 1..120 seconds; zero selects 120. Capture buffers at most
  120 seconds of mono float32 samples. Both helper and Go enforce a recording
  ceiling. Go requires stop acknowledgement within 2 seconds; the owned process
  lifetime also bounds model load/transcription to duration plus 120 seconds.
- Each call has a fresh random generation. Wrong-generation, duplicate-key,
  oversized, out-of-order or incomplete output is rejected. JSONL frames are
  bounded to 64 KiB, total output to 256 KiB, transcript to 32 KiB.
- Canceling owns the child tree through `internal/process`; the package has no
  alternative launcher. Windows uses a hidden owned Job Object; Linux uses the
  existing process-group ownership implementation. macOS process launch remains
  explicitly unsupported. Cross-compilation is not native support evidence.
- A named microphone must match exactly one input device. Failure does not select
  another microphone. Hardware permissions, PortAudio availability and support
  for 16 kHz input remain host prerequisites. There is no cross-process microphone
  lease with Murmur/Hub in this slice.
- Each call reloads its model. Streaming inference, persistent model workers,
  GPU selection and measured transcription latency are deferred until real
  inference and cancellation evidence justify them. Transcript/audio are not
  logged or written by this package; raw native diagnostics are not copied.

## Verification

From the repository root, using its pinned Go toolchain/cache route:

```powershell
go test -p=2 -count=1 -timeout=60s ./internal/voice
go vet ./internal/voice
# Optional: a trusted existing CPython with stdlib, no installed ASR needed.
$env:UCLI_TEST_VOICE_PYTHON = 'C:/absolute/python.exe'
go test -p=2 -count=1 -timeout=60s -v ./internal/voice
```

The normal suite launches actual owned Go test children as deterministic helper
fixtures. It exercises literal results, malformed/stale replies, nonzero exit,
late results, cancellation and process death, stop acknowledgement, duration
ceiling, blocked feedback and preflight rejection. The optional route executes
the **embedded Python helper** with synthetic PCM and explicit fake inference and
capture modules. It checks offline/local model arguments, library-log suppression,
manual-site provenance, inert `.pth` files and both explicit/helper-limit stop.
It never activates a microphone, loads a real model or downloads packages.

The [Windows fixture receipt](../../docs/verification.md) preserves the latest
run. The host's Windows Store Python alias was rejected as nonregular; selecting
its Program Files executable reached `Access is denied` in owned launch. An
explicit existing RaddishHub embedded Python successfully exercised the helper
fixtures. These failures remain runtime-selection evidence, not a reason to
silently change an explicit interpreter pin. Real audio quality, actual local
inference, physical microphone/permissions, Linux native execution and macOS
process support remain unverified.

## Reference and licensing

The local Murmur project informed the seams (16 kHz audio, explicit stop,
generation fencing and transcript-only output). No Murmur source is copied or
imported; its repository had no tracked license to authorize code reuse at this
audit. This helper is a fresh implementation of upstream APIs.

Upstream runtime and model acquisition must retain applicable notices:
[faster-whisper (MIT)](https://github.com/SYSTRAN/faster-whisper/blob/master/LICENSE),
[CTranslate2 (MIT)](https://github.com/OpenNMT/CTranslate2/blob/master/LICENSE),
[sounddevice and PortAudio installation](https://python-sounddevice.readthedocs.io/en/0.5.1/installation.html),
and the [selected converted model's own license](https://huggingface.co/Systran/faster-whisper-base.en).
Those references describe dependencies; this package bundles no model or wheels.
