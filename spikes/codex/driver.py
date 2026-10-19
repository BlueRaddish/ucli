"""Bounded P02 experiment, not a production adapter or a security sandbox.

Native launch is deliberately metadata-only until binding/trust gates are proved.
Fixtures consume the same transport and lifecycle methods as the native probe.
"""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import queue
import sys
import threading
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from shared.owned_process import OwnedProcess

MAX_FRAME = 1024 * 1024
MAX_EVENTS = 512


class ProtocolError(RuntimeError):
    pass


class Deadline(ProtocolError):
    pass


class SessionBusy(RuntimeError):
    pass


class SessionLease:
    """Local cooperative lock; deliberately never steal stale/uncertain leases.

    Ceiling: no fencing against native clients or other hosts. P08 must replace
    this with durable ownership and reconciliation before claiming crash recovery.
    """

    def __init__(self, directory: Path, scope: dict):
        self.scope = scope
        directory.mkdir(parents=True, exist_ok=True)
        digest = hashlib.sha256(json.dumps(scope, sort_keys=True).encode()).hexdigest()
        self.path = directory / (digest + ".lease")
        self.held = False

    def acquire(self):
        try:
            descriptor = os.open(self.path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except FileExistsError:
            raise SessionBusy("session already owned or awaiting reconciliation") from None
        with os.fdopen(descriptor, "w") as stream:
            # No native session/account identifiers are persisted.
            stream.write('{"state":"owned-or-uncertain"}\n')
        self.held = True

    def release_confirmed(self):
        if self.held:
            self.path.unlink()
            self.held = False


class Driver:
    def __init__(self, command: list[str], cwd: Path, env: dict | None = None, allow_turns=False):
        self.child = OwnedProcess(command, cwd, env)
        self.queue = queue.Queue(maxsize=128)
        self.reader_failure = None
        self.events = []
        self.next_id = 0
        self.stderr_bytes = 0
        self.sent_methods = []
        self.allow_turns = allow_turns
        self.dispatched = False
        self.terminal_confirmed = False
        self.active = None
        self.thread_policy = None
        self.transport_failed = False
        self.rpc_lock = threading.Lock()
        self.threads = [threading.Thread(target=self._read_stdout, daemon=True),
                        threading.Thread(target=self._drain_stderr, daemon=True)]
        for thread in self.threads:
            thread.start()

    def _read_stdout(self):
        try:
            buffer = bytearray()
            while True:
                chunk = self.child.process.stdout.read(4096)
                if not chunk:
                    if buffer:
                        raise ProtocolError("truncated frame")
                    self.queue.put_nowait(None)
                    return
                buffer.extend(chunk)
                while b"\n" in buffer:
                    raw, _, remainder = buffer.partition(b"\n")
                    buffer = bytearray(remainder)
                    if len(raw) > MAX_FRAME:
                        raise ProtocolError("frame too large")
                    try:
                        value = json.loads(raw)
                    except (ValueError, UnicodeDecodeError, RecursionError):
                        raise ProtocolError("malformed JSON frame") from None
                    if not isinstance(value, dict):
                        raise ProtocolError("frame must be an object")
                    self.queue.put_nowait(value)
                if len(buffer) > MAX_FRAME:
                    raise ProtocolError("frame too large")
        except queue.Full:
            self.reader_failure = ProtocolError("bounded input queue overflow")
        except (ProtocolError, OSError, ValueError) as error:
            self.reader_failure = error if isinstance(error, ProtocolError) else ProtocolError("stdout closed")

    def _drain_stderr(self):
        try:
            while True:
                chunk = self.child.process.stderr.read(4096)
                if not chunk:
                    return
                self.stderr_bytes += len(chunk)  # Never retain native diagnostic content.
        except (OSError, ValueError):
            pass

    def _send(self, frame):
        encoded = json.dumps(frame, ensure_ascii=True, separators=(",", ":")).encode() + b"\n"
        if len(encoded) > 65536:
            raise ProtocolError("outbound frame too large for bounded spike")
        completed = threading.Event()
        failure = []
        def write():
            try:
                remaining = memoryview(encoded)
                while remaining:
                    sent = self.child.process.stdin.write(remaining)
                    if not sent:
                        raise BrokenPipeError()
                    remaining = remaining[sent:]
                self.child.process.stdin.flush()
            except (BrokenPipeError, OSError, ValueError):
                failure.append(True)
            finally:
                completed.set()
        threading.Thread(target=write, daemon=True).start()
        if not completed.wait(2):
            self.transport_failed = True
            raise Deadline("transport write deadline; dispatch may be uncertain")
        if failure:
            self.transport_failed = True
            raise ProtocolError("transport write failed; dispatch may be uncertain")

    def _next(self, deadline):
        while time.monotonic() < deadline:
            if self.reader_failure:
                raise self.reader_failure
            try:
                frame = self.queue.get(timeout=min(0.05, max(0.001, deadline - time.monotonic())))
            except queue.Empty:
                continue
            if frame is None:
                raise ProtocolError("transport EOF")
            return frame
        raise Deadline("terminal deadline exceeded; remote effects may be unknown")

    def _event(self, frame):
        method = frame.get("method")
        if not isinstance(method, str):
            raise ProtocolError("unexpected response or missing method")
        if "id" in frame:
            if method in ("item/commandExecution/requestApproval", "item/fileChange/requestApproval"):
                self._send({"id": frame["id"], "result": {"decision": "decline"}})
            else:
                self._send({"id": frame["id"], "error": {"code": -32601, "message": "unsupported server request"}})
        if len(self.events) >= MAX_EVENTS:
            raise ProtocolError("bounded event limit exceeded")
        self.events.append(frame)

    def request(self, method, params=None, timeout=10):
        if self.transport_failed:
            raise ProtocolError("transport must be closed after failed write")
        if not self.rpc_lock.acquire(blocking=False):
            raise SessionBusy("one RPC at a time per spike connection")
        try:
            return self._request(method, params, timeout)
        finally:
            self.rpc_lock.release()

    def _request(self, method, params, timeout):
        if method == "turn/start" and not self.allow_turns:
            raise ProtocolError("live inference blocked: isolated account binding and trust unproved")
        self.next_id += 1
        request_id = self.next_id
        self.sent_methods.append(method)
        if method == "turn/start":
            # Record before writing: a broken write cannot prove no remote acceptance.
            self.dispatched = True
            self.terminal_confirmed = False
        self._send({"id": request_id, "method": method, "params": params or {}})
        deadline = time.monotonic() + timeout
        while True:
            frame = self._next(deadline)
            if frame.get("id") == request_id and "method" not in frame:
                if "error" in frame:
                    # Error messages can contain account/config data; only retain typed code.
                    raise ProtocolError("native request error code " + str(frame["error"].get("code")))
                if "result" not in frame:
                    raise ProtocolError("response lacks result")
                return frame["result"]
            self._event(frame)

    def initialize(self):
        result = self.request("initialize", {"clientInfo": {"name": "ucli_p02_spike", "version": "0.1"}})
        self._send({"method": "initialized", "params": {}})
        return result

    def start_thread(self, cwd: Path):
        result = self.request("thread/start", {"cwd": str(cwd.resolve()), "approvalPolicy": "never",
                                              "sandbox": "read-only", "ephemeral": False})
        self.thread_policy = {"approval_policy": result.get("approvalPolicy"),
                              "sandbox_type": (result.get("sandbox") or {}).get("type"),
                              "instruction_source_count": len(result.get("instructionSources", [])),
                              "ucli_instruction_injection": False}
        return result["thread"]["id"]

    def observe(self, seconds=1):
        deadline = time.monotonic() + seconds
        try:
            while True:
                self._event(self._next(deadline))
        except Deadline:
            return

    def resume(self, thread_id, cwd: Path, lease: SessionLease):
        self._check_lease(thread_id, lease)
        if self.active or (self.dispatched and not self.terminal_confirmed):
            raise SessionBusy("cannot resume while a turn is active or uncertain")
        return self.request("thread/resume", {"threadId": thread_id, "cwd": str(cwd.resolve()),
                                             "excludeTurns": True, "approvalPolicy": "never", "sandbox": "read-only"})

    def _check_lease(self, thread_id, lease):
        if not lease.held or lease.scope.get("thread") != thread_id:
            raise SessionBusy("matching lease required before session mutation")

    def start_turn(self, thread_id, text, lease: SessionLease):
        self._check_lease(thread_id, lease)
        if self.active or (self.dispatched and not self.terminal_confirmed):
            raise SessionBusy("active turn must reach a confirmed terminal state")
        result = self.request("turn/start", {"threadId": thread_id,
                                             "input": [{"type": "text", "text": text}]})
        self.active = (thread_id, result["turn"]["id"])
        return self.active[1]

    def interrupt(self):
        if self.active:
            return self.request("turn/interrupt", {"threadId": self.active[0], "turnId": self.active[1]})

    def wait_terminal(self, timeout=10):
        deadline = time.monotonic() + timeout
        cursor = 0
        while True:
            while cursor < len(self.events):
                event = self.events[cursor]
                cursor += 1
                if event.get("method") == "turn/completed":
                    params = event.get("params", {})
                    turn = params.get("turn", {})
                    if self.active == (params.get("threadId"), turn.get("id")):
                        status = turn.get("status")
                        if status not in ("completed", "interrupted", "failed"):
                            raise ProtocolError("unrecognized terminal status")
                        self.terminal_confirmed = True
                        self.active = None
                        return status
            self._event(self._next(deadline))

    @property
    def external_effects(self):
        return "unknown" if self.dispatched and not self.terminal_confirmed else "not-unknown-by-transport"

    def close(self):
        self.child.close()
        for thread in self.threads:
            thread.join(timeout=1)


def sanitized_account(result):
    account = result.get("account")
    return {"present": isinstance(account, dict),
            "auth_mode": account.get("type") if isinstance(account, dict) else "none",
            "identity_available": bool(account and (account.get("email") or result.get("workspaceRouting"))),
            "identity_persisted": False, "binding_verified": False,
            "inference_entitlement": "unverified"}


def sanitized_models(result):
    # Catalog is discovery, never evidence of entitlement/account binding.
    models = result.get("data", [])
    return {"count": len(models), "ids": [m["id"] for m in models if isinstance(m.get("id"), str)],
            "has_next_page": bool(result.get("nextCursor")), "inference_entitlement": "unverified"}
