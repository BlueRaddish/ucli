"""Bounded second-runtime experiment. Native Muse uses its no-inference echo backend."""
from __future__ import annotations

import argparse
from collections import deque
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import queue
import secrets
import subprocess
import sys
import tempfile
import threading
import time
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from shared.owned_process import OwnedProcess

MAX_FRAME = 1024 * 1024
MAX_EVENTS = 2048
MAX_SESSION_BYTES = 16 * MAX_FRAME  # Spike ceiling; production must persist/spill long transcripts.


class ProtocolError(RuntimeError):
    pass


def command_id() -> str:
    value = (int(time.time() * 1000) << 80) | (7 << 76)
    value |= secrets.randbits(12) << 64 | 2 << 62 | secrets.randbits(62)
    return str(uuid.UUID(int=value))


def isolated_environment(root: Path) -> dict[str, str]:
    env = {key: os.environ[key] for key in
           ("SystemRoot", "WINDIR", "COMSPEC", "PATH", "PATHEXT", "LANG")
           if key in os.environ}
    for key, name in {"HOME": "home", "USERPROFILE": "home", "APPDATA": "config",
                      "LOCALAPPDATA": "local", "XDG_CONFIG_HOME": "config",
                      "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state",
                      "XDG_CACHE_HOME": "cache", "TEMP": "tmp", "TMP": "tmp"}.items():
        target = root / name
        target.mkdir(exist_ok=True)
        env[key] = str(target)
    env["MUSE_AUTH_PATH"] = str(root / "absent-auth.json")
    env["MUSE_NO_AUTO_UPDATE"] = "1"
    return env


class SessionLease:
    """Crash leaves a lease for reconciliation; no timestamp-based auto-takeover."""
    def __init__(self, directory: Path, scope: str):
        directory.mkdir(parents=True, exist_ok=True)
        self.path = directory / (hashlib.sha256(scope.encode()).hexdigest() + ".lock")
        self.token = secrets.token_hex(16)

    def __enter__(self):
        fd = os.open(self.path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as file:
            file.write(self.token)
        return self

    def __exit__(self, *unused):
        if unused and unused[0] is not None:
            return  # Unknown external outcome retains the fence for reconciliation.
        if self.path.read_text(encoding="utf-8") != self.token:
            raise ProtocolError("session lease ownership changed")
        self.path.unlink()


class RPC:
    """One connection, one consuming caller, bounded frames/queues and deadlines."""
    def __init__(self, args: list[str], cwd: Path, env: dict[str, str], timeout=8):
        self.timeout = timeout
        self.next_id = 0
        self.events = deque()
        self.responses = {}
        self.frames = queue.Queue(MAX_EVENTS)
        self.fatal = threading.Event()
        self.closed = False
        self.stderr_size = 0
        self.transport_failed = False
        self.child = OwnedProcess(args, cwd, env)
        self.process = self.child.process
        self.output = io.BufferedReader(self.process.stdout)
        self.readers = [threading.Thread(target=self._read_stdout, daemon=True),
                        threading.Thread(target=self._drain_stderr, daemon=True)]
        for reader in self.readers:
            reader.start()

    def _offer(self, value):
        try:
            self.frames.put_nowait(value)
        except queue.Full:
            self.fatal.set()

    def _read_stdout(self):
        total = 0
        try:
            while True:
                line = self.output.readline(MAX_FRAME + 1)
                if not line:
                    self._offer(EOFError("runtime stdout closed"))
                    return
                if len(line) > MAX_FRAME:
                    raise ProtocolError("frame exceeds limit")
                total += len(line)
                if total > MAX_SESSION_BYTES:
                    raise ProtocolError("spike session byte ceiling exceeded")
                try:
                    value = json.loads(line.decode("utf-8"))
                except (ValueError, UnicodeError, RecursionError) as error:
                    raise ProtocolError("malformed UTF-8/JSON frame") from error
                if not isinstance(value, dict) or value.get("jsonrpc") != "2.0":
                    raise ProtocolError("invalid JSON-RPC envelope")
                if "method" in value and not isinstance(value["method"], str):
                    raise ProtocolError("invalid method")
                if "id" in value and (isinstance(value["id"], bool) or not isinstance(value["id"], (str, int))):
                    raise ProtocolError("invalid response/request ID")
                if "result" in value and "error" in value:
                    raise ProtocolError("ambiguous result/error envelope")
                if "error" in value and not isinstance(value["error"], dict):
                    raise ProtocolError("invalid error object")
                if "method" in value and not isinstance(value.get("params", {}), dict):
                    raise ProtocolError("invalid named method parameters")
                self._offer(value)
        except Exception as error:
            self._offer(error)

    def _drain_stderr(self):
        while True:
            block = self.process.stderr.read(4096)
            if not block:
                return
            # Count, never retain potentially sensitive native diagnostics.
            self.stderr_size += len(block)

    def _receive(self, deadline):
        while True:
            if self.fatal.is_set():
                raise ProtocolError("event queue exceeds limit")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("runtime response deadline exceeded; external outcome unknown")
            try:
                value = self.frames.get(timeout=min(remaining, 0.1))
                break
            except queue.Empty:
                continue
        if isinstance(value, Exception):
            raise value
        if "id" in value:
            # Server requests are refused: this probe cannot approve native actions.
            if "method" in value:
                self.send({"jsonrpc": "2.0", "id": value["id"],
                           "error": {"code": -32601, "message": "probe does not grant authority"}})
            else:
                if len(self.responses) >= MAX_EVENTS:
                    raise ProtocolError("unmatched response limit exceeded")
                self.responses[value["id"]] = value
        elif "method" in value:
            if len(self.events) >= MAX_EVENTS:
                raise ProtocolError("notification limit exceeded")
            self.events.append(value)
        else:
            raise ProtocolError("frame is neither response nor notification")

    def send(self, value, deadline=None):
        if self.transport_failed:
            raise ProtocolError("transport poisoned after uncertain write")
        raw = (json.dumps(value, ensure_ascii=False) + "\n").encode("utf-8")
        if len(raw) > MAX_FRAME:
            raise ProtocolError("outgoing frame exceeds limit")
        finished = threading.Event()
        errors = []

        def write():
            try:
                remaining = memoryview(raw)
                while remaining:
                    sent = self.process.stdin.write(remaining)
                    if not sent:
                        raise BrokenPipeError()
                    remaining = remaining[sent:]
                self.process.stdin.flush()
            except (OSError, ValueError):
                errors.append(True)
            finally:
                finished.set()

        threading.Thread(target=write, daemon=True).start()
        deadline = deadline if deadline is not None else time.monotonic() + self.timeout
        if not finished.wait(max(0, deadline - time.monotonic())):
            self.transport_failed = True
            raise TimeoutError("write deadline exceeded; external outcome unknown")
        if errors:
            self.transport_failed = True
            raise ProtocolError("transport write failed; external outcome unknown")

    def notify(self, method):
        self.send({"jsonrpc": "2.0", "method": method})

    def call(self, method, params):
        self.next_id += 1
        request_id = self.next_id
        deadline = time.monotonic() + self.timeout
        self.send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params}, deadline)
        while request_id not in self.responses:
            self._receive(deadline)
        response = self.responses.pop(request_id)
        if "error" in response:
            raise ProtocolError(f"{method}: native RPC error {response['error'].get('code')}")
        if "result" not in response:
            raise ProtocolError("response lacks result")
        if not isinstance(response["result"], dict):
            raise ProtocolError("MSP method result must be an object")
        return response["result"]

    def terminal(self, session_id, turn_id):
        deadline = time.monotonic() + self.timeout
        text = []
        while True:
            while self.events:
                event = self.events.popleft()
                params = event.get("params", {})
                if params.get("sessionId") != session_id:
                    continue
                if event["method"] == "item/delta":
                    text.append(params.get("delta", ""))
                if event["method"] == "turn/completed" and params.get("turnId") == turn_id:
                    if params.get("terminal") not in ("completed", "failed", "cancelled"):
                        raise ProtocolError("unknown terminal state requires reconciliation")
                    return {"terminal": params.get("terminal"), "text": "".join(text)}
            self._receive(deadline)

    def close(self):
        if self.closed:
            return
        self.closed = True
        try:
            if not self.transport_failed:
                self.process.stdin.close()  # Unbuffered pipe: no pending buffered write to flush.
                self.process.wait(timeout=3)
        except (OSError, subprocess.TimeoutExpired):
            pass
        finally:
            self.child.close()  # Kill only the owned job/group, including inherited pipes.
        for reader in self.readers:
            reader.join(timeout=1)
        if not self.readers[0].is_alive():
            self.output.close()

    def __enter__(self):
        return self

    def __exit__(self, *unused):
        self.close()


def normalize_agy_result(events: list[dict], *, stderr="", process_exit=0) -> dict:
    """Never interpret an empty SUCCESS or a timed-out partial turn as completion."""
    if any(not isinstance(event, dict) for event in events):
        raise ProtocolError("invalid stream event")
    results = [e.get("result") for e in events if e.get("event") == "result"]
    if len(results) != 1:
        raise ProtocolError("one terminal result required for a one-turn run")
    result = results[0]
    if not isinstance(result, dict):
        raise ProtocolError("invalid terminal result")
    if "print timeout" in stderr.lower():
        return {"status": "unknown", "reason": "native_timeout_partial"}
    if process_exit or result.get("status") != "SUCCESS" or result.get("error"):
        return {"status": "failed", "reason": "native_failure"}
    response = result.get("response")
    structured = result.get("structured_output")
    if not (isinstance(response, str) and response.strip()) and structured is None:
        return {"status": "unknown", "reason": "empty_success"}
    return {"status": "complete", "text": response, "structured_output": structured}


def muse_probe(executable: Path) -> dict:
    executable = executable.resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="ucli-muse-spike-") as temporary:
        root = Path(temporary)
        workspace = root / "workspace"
        workspace.mkdir()
        env = isolated_environment(root)
        command = [str(executable), "serve", "--provider", "echo", "--no-session-log",
                   "--disable-write", "--disable-shell"]
        with RPC(command, workspace, env) as rpc:
            hello = rpc.call("initialize", {"clientInfo": {"name": "ucli_spike", "version": "0.0.0"},
                                            "capabilities": {"userInputDialogs": False}})
            rpc.notify("initialized")
            create = {"commandId": command_id(), "workspaceRoot": str(workspace), "providerId": "echo"}
            first = rpc.call("session/start", create)
            session_id = first["session"]["sessionId"]
            duplicate = rpc.call("session/start", create)
            if duplicate["session"]["sessionId"] != session_id:
                raise ProtocolError("duplicate command created another session")
            outcomes = []
            with SessionLease(root / "leases", str(executable) + ":none:" + session_id):
                for marker in ("ucli-fixture-one", "ucli-fixture-two"):
                    ack = rpc.call("turn/start", {"commandId": command_id(), "sessionId": session_id,
                                                  "input": [{"type": "text", "text": marker}]})
                    outcome = rpc.terminal(session_id, ack["turnId"])
                    outcomes.append({"terminal": outcome["terminal"], "marker_seen": marker in outcome["text"]})
                ack = rpc.call("turn/start", {"commandId": command_id(), "sessionId": session_id,
                                              "input": [{"type": "text", "text": "ucli-interrupt-marker"}]})
                stopped = rpc.call("turn/interrupt", {"commandId": command_id(), "sessionId": session_id,
                                                     "turnId": ack["turnId"]})
                interrupt = rpc.terminal(session_id, ack["turnId"])
            return {"observed_utc": datetime.now(timezone.utc).isoformat(),
                    "runtime": "muse", "backend": "native-echo", "inference_executed": False,
                    "executable_sha256": hashlib.sha256(executable.read_bytes()).hexdigest(),
                    "schema": hello.get("schema"), "platform": hello.get("platformOs"),
                    "session_command_deduplicated": True, "sequential_turns": outcomes,
                    "interrupt_acknowledged": stopped.get("turnId") == ack["turnId"],
                    "interrupt_terminal": interrupt["terminal"],
                    "interruption_observed": interrupt["terminal"] == "cancelled",
                    "session_durability": hello.get("sessionDurability"),
                    "sandbox_availability": hello.get("sandboxAvailability", "unknown"),
                    "account_binding": "none: isolated echo backend",
                    "unverified": ["Meta inference and identity", "permission enforcement against hostile tools",
                                   "native startup hook suppression", "cross-process native session contention",
                                   "persistent native resume", "provider quota", "macOS/Linux execution"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--muse", type=Path, required=True, help="explicit installed native binary, bypasses auto-updating shim")
    parser.add_argument("--receipt", type=Path)
    args = parser.parse_args()
    try:
        receipt = muse_probe(args.muse)
        okay = all(t["terminal"] == "completed" and t["marker_seen"] for t in receipt["sequential_turns"])
        receipt["probe_passed"] = okay
        text = json.dumps(receipt, indent=2) + "\n"
        if args.receipt:
            args.receipt.parent.mkdir(parents=True, exist_ok=True)
            args.receipt.write_text(text, encoding="utf-8")
        print(text)
        return 0 if okay else 1
    except (OSError, ProtocolError, TimeoutError, EOFError, KeyError) as error:
        print(f"second-runtime probe failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
