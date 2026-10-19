"""UCLI's invocation-only local ASR helper. No Murmur source is imported/copied.

Only the explicit main() call imports inference/capture dependencies. stdin and
stdout carry bounded version1 JSONL; stdout never carries logs or audio.
"""
import contextlib
import importlib.metadata
import json
import os
import pathlib
import sys
import threading
import wave

MAX_FRAME = 65536
RATE = 16000
WIRE = sys.stdout


class VoiceError(Exception):
    pass


def emit(generation, kind, **values):
    value = dict(version=1, generation=generation, type=kind, **values)
    line = json.dumps(value, ensure_ascii=False, allow_nan=False)
    if len(line.encode("utf-8")) >= MAX_FRAME:
        raise VoiceError("transcription_failed")
    print(line, file=WIRE, flush=True)


def read_request():
    # Only two short control messages exist per take. Raw byte reads avoid a
    # daemon thread retaining BufferedReader's lock during interpreter shutdown
    # when the helper's own recording limit wins the race with the Go timer.
    raw = bytearray()
    while len(raw) <= MAX_FRAME:
        value = os.read(sys.stdin.fileno(), 1)
        raw.extend(value)
        if not value or value == b"\n":
            break
    if len(raw) > MAX_FRAME or not raw.endswith(b"\n"):
        raise VoiceError("request_invalid")
    def unique(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise VoiceError("request_invalid")
            value[key] = item
        return value
    try:
        return json.loads(raw, object_pairs_hook=unique)
    except (ValueError, UnicodeError):
        raise VoiceError("request_invalid") from None


def validate(request):
    allowed = {"version", "generation", "mode", "model_dir", "audio_path", "language", "device", "max_seconds"}
    if not isinstance(request, dict) or set(request) - allowed:
        raise VoiceError("request_invalid")
    generation = request.get("generation", "")
    if (request.get("version") != 1 or type(request.get("version")) is not int
            or not isinstance(generation, str) or len(generation) != 32
            or any(c not in "0123456789abcdef" for c in generation)
            or request.get("mode") not in {"file", "capture"}
            or type(request.get("max_seconds")) not in (int, float)
            or not 1 <= request["max_seconds"] <= 120):
        raise VoiceError("request_invalid")
    for field in ("model_dir", "audio_path", "language", "device"):
        value = request.get(field, "")
        if not isinstance(value, str) or "\0" in value:
            raise VoiceError("request_invalid")
    if len(request.get("language", "")) > 16 or len(request.get("device", "")) > 256:
        raise VoiceError("request_invalid")
    model = pathlib.Path(request["model_dir"])
    if not model.is_absolute() or not model.is_dir() or model.is_symlink():
        raise VoiceError("model_invalid")
    for name in ("config.json", "model.bin", "tokenizer.json", "vocabulary.txt"):
        file = model / name
        if not file.is_file() or file.is_symlink() or file.stat().st_size == 0:
            raise VoiceError("model_invalid")


def read_wave(path, limit):
    source = pathlib.Path(path)
    if not source.is_absolute() or not source.is_file() or source.is_symlink() or source.stat().st_size > 8 * 1024 * 1024:
        raise VoiceError("audio_invalid")
    try:
        with wave.open(str(source), "rb") as audio:
            frames = audio.getnframes()
            if (audio.getnchannels() != 1 or audio.getsampwidth() != 2
                    or audio.getframerate() != RATE or audio.getcomptype() != "NONE"
                    or frames > int(RATE * limit)):
                raise VoiceError("audio_invalid")
            data = audio.readframes(frames)
            if len(data) != frames * 2:
                raise VoiceError("audio_invalid")
            return data
    except (wave.Error, EOFError, OSError):
        raise VoiceError("audio_invalid") from None


def dependencies(capture):
    names = ["faster-whisper", "ctranslate2", "numpy", "tokenizers", "huggingface-hub"]
    if capture:
        names.append("sounddevice")
    try:
        versions = {name: importlib.metadata.version(name) for name in names}
    except importlib.metadata.PackageNotFoundError:
        raise VoiceError("dependencies_missing") from None
    if sys.version_info < (3, 9) or not versions["faster-whisper"].startswith("1.") or not versions["ctranslate2"].startswith("4."):
        raise VoiceError("dependencies_incompatible")
    versions["python"] = sys.version.split()[0]
    try:
        import numpy as np
        from faster_whisper import WhisperModel
    except ImportError:
        raise VoiceError("dependencies_missing") from None
    return versions, np, WhisperModel


def capture_audio(request, stopped, np):
    try:
        import sounddevice as sd
        device = None
        requested = request.get("device", "")
        if requested:
            matches = [i for i, item in enumerate(sd.query_devices()) if item["max_input_channels"] > 0 and item["name"] == requested]
            if len(matches) != 1:
                raise VoiceError("capture_unavailable")
            device = matches[0]
        name = sd.query_devices(device, "input")["name"]
        chunks, failures = [], []
        count = 0
        maximum = int(RATE * request["max_seconds"])
        def receive(data, frames, timing, status):
            nonlocal count
            if status:
                failures.append(True)
                stopped.set()
                raise sd.CallbackStop
            take = min(frames, maximum - count)
            if take:
                chunks.append(data[:take, 0].copy())
                count += take
            if count >= maximum:
                stopped.set()
                raise sd.CallbackStop
        if stopped.is_set():
            return None
        with sd.InputStream(samplerate=RATE, channels=1, dtype="float32", device=device, callback=receive):
            emit(request["generation"], "recording", device=name)
            stopped.wait(request["max_seconds"])
        emit(request["generation"], "capture_stopped")
        if failures:
            raise VoiceError("capture_overflow")
        return np.concatenate(chunks) if chunks else np.zeros(0, dtype=np.float32)
    except VoiceError:
        raise
    except Exception:
        raise VoiceError("capture_unavailable") from None


def main():
    generation = ""
    WIRE.reconfigure(encoding="utf-8")
    try:
        os.environ["HF_HUB_OFFLINE"] = "1"
        os.environ["TRANSFORMERS_OFFLINE"] = "1"
        os.environ["HF_HUB_DISABLE_TELEMETRY"] = "1"
        site = ""
        if len(sys.argv) > 1:
            if len(sys.argv) != 3 or sys.argv[1] != "--site-dir":
                raise VoiceError("request_invalid")
            site = sys.argv[2]
            if not os.path.isabs(site) or not os.path.isdir(site) or os.path.islink(site):
                raise VoiceError("request_invalid")
            # Do not call site.addsitedir: this added directory's .pth files
            # never run. The explicitly selected interpreter's startup is trusted.
            sys.path.insert(0, site)
        request = read_request()
        generation = request.get("generation", "") if isinstance(request, dict) else ""
        validate(request)
        mode = request["mode"]
        pcm = read_wave(request["audio_path"], request["max_seconds"]) if mode == "file" else None
        stopped = threading.Event()
        if mode == "capture":
            def controls():
                try:
                    command = read_request()
                    if command != {"version": 1, "generation": generation, "type": "stop"}:
                        os._exit(2) # Never continue recording after corrupt control input.
                    stopped.set()
                except Exception:
                    os._exit(2)
            threading.Thread(target=controls, daemon=True).start()
        emit(generation, "loading")
        # Dependencies may log during import/load; only this helper writes JSONL.
        with open(os.devnull, "w") as quiet, contextlib.redirect_stdout(quiet), contextlib.redirect_stderr(quiet):
            versions, np, model_type = dependencies(mode == "capture")
            try:
                model = model_type(request["model_dir"], device="cpu", compute_type="int8", cpu_threads=4, local_files_only=True)
            except Exception:
                raise VoiceError("model_invalid") from None
            audio = np.frombuffer(pcm, dtype="<i2").astype(np.float32) / 32768.0 if mode == "file" else capture_audio(request, stopped, np)
            if audio is None:
                emit(generation, "canceled")
                return 0
            emit(generation, "transcribing")
            duration = len(audio) / RATE
            language, text = request.get("language", ""), ""
            if len(audio):
                segments, info = model.transcribe(audio, language=language or None, beam_size=5, temperature=0.0, vad_filter=True, condition_on_previous_text=False)
                pieces, size = [], 0
                for segment in segments:
                    size += len(segment.text.encode("utf-8"))
                    if size > 32000:
                        raise VoiceError("transcription_failed")
                    pieces.append(segment.text)
                text = "".join(pieces).strip()
                language = info.language
        emit(generation, "result", text=text, language=language, duration_seconds=duration, versions=versions, site_dir=site)
        return 0
    except VoiceError as error:
        emit(generation, "error", code=str(error))
    except Exception:
        emit(generation, "error", code="transcription_failed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
