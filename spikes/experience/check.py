"""Consume the actual fixture CLI through pipes; no provider calls or test framework."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import queue
import subprocess
import sys
import tempfile
import threading
import time


ROOT = Path(__file__).resolve().parent
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
ENV = dict(os.environ, PYTHONIOENCODING="utf-8", PYTHONUTF8="1")
CHECKS = []
TRANSCRIPTS = {}


def run(state, script, expected=0):
    completed = subprocess.run(
        [sys.executable, str(ROOT / "prototype.py"), "--state", str(state)],
        input=script, text=True, encoding="utf-8", capture_output=True,
        timeout=10, creationflags=FLAGS, env=ENV,
    )
    assert completed.returncode == expected, (completed.returncode, completed.stdout, completed.stderr)
    assert "\x1b" not in completed.stdout, "terminal control sequence escaped output"
    return completed.stdout, completed.stderr


def read(state):
    return json.loads(state.read_text(encoding="utf-8"))


def checked(name, action):
    action()
    CHECKS.append({"name": name, "result": "pass"})
    print("PASS " + name, flush=True)


class Session:
    def __init__(self, state):
        self.process = subprocess.Popen(
            [sys.executable, str(ROOT / "prototype.py"), "--state", str(state)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            text=True, encoding="utf-8", creationflags=FLAGS, env=ENV,
        )
        self.lines = []
        self.incoming = queue.Queue()
        self.reader = threading.Thread(target=self.pump, daemon=True)
        self.reader.start()

    def pump(self):
        for line in self.process.stdout:
            self.incoming.put(line)
        self.incoming.put(None)

    def send(self, text):
        self.process.stdin.write(text)
        self.process.stdin.flush()

    def until(self, phrase):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            try:
                line = self.incoming.get(timeout=max(0.01, deadline - time.monotonic()))
            except queue.Empty:
                break
            if line is None:
                break
            self.lines.append(line)
            if phrase in line:
                return
        raise AssertionError(f"Missing output {phrase!r}; observed: {''.join(self.lines)}")

    def close(self, kill=False):
        if kill:
            self.process.terminate()
        else:
            self.process.stdin.close()
        self.process.wait(timeout=5)
        self.reader.join(timeout=5)
        while not self.incoming.empty():
            line = self.incoming.get()
            if line is not None:
                self.lines.append(line)
        self.process.stdout.close()
        return "".join(self.lines)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--receipt", type=Path)
    args = parser.parse_args()
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="ucli-p05-check-") as scratch:
        folder = Path(scratch)

        def ordinary():
            state = folder / "ordinary.json"
            output, _ = run(state, "Fix offline tests\nAlso handle empty responses\n/result\n/new\nWrite a note\n/tasks\n1\n/result\n/exit\n")
            tasks = read(state)["tasks"]
            assert len(tasks) == 2 and len(tasks[0]["attempts"]) == 2
            assert [a["revision"] for a in tasks[0]["attempts"]] == [1, 2]
            assert tasks[0]["attempts"][0]["prompt"] == "Fix offline tests"
            assert "Follow up on this task" in output and "Opened T1; no attempt started" in output
            assert "T1 attempt 1 revision 1 [complete]" in output
            TRANSCRIPTS["ordinary"] = output
        checked("start, follow up, new task, inspect previous results", ordinary)

        def choices():
            state = folder / "choices.json"
            output, _ = run(state, "/models\ndeliberate\n1\n/accounts\n3\n/effort\n4\nReview changes\n/models\n2\n/effort\n3\nAdd a short check\n/new\nFresh task\n/exit\n")
            tasks = read(state)["tasks"]
            first, second = tasks[0]["attempts"]
            assert first["settings"]["model"] == "fixture/deliberate"
            assert first["settings"]["effort"] == "high" and first["settings"]["account"] == "sample-work"
            assert second["settings"]["model"] == "fixture/sprint"
            assert "Runtime changed: bounded fixture handoff" in output
            assert tasks[1]["attempts"][0]["settings"]["account"] == "sample-personal"
            TRANSCRIPTS["choices"] = output
        checked("visible searchable choices, runtime difference, session scope", choices)

        def login():
            state = folder / "login.json"
            first, _ = run(state, "/accounts\n3\n/scenario\n2\nPreserve this login draft\n/exit\n")
            data = read(state)
            assert data["tasks"] == [] and data["drafts"]["new"]["text"] == "Preserve this login draft"
            assert "nothing dispatched" in first
            second, _ = run(state, "/reconnect\n/send\n/exit\n")
            assert "Saved draft: Preserve this login draft" in second
            attempt = read(state)["tasks"][0]["attempts"][0]
            assert attempt["prompt"] == "Preserve this login draft" and attempt["settings"]["account"] == "sample-work"
            TRANSCRIPTS["login-restart"] = first + "\n--- RESTART ---\n" + second
        checked("failed login and restart retain draft and selected account", login)

        def failed_provider():
            state = folder / "provider.json"
            output, _ = run(state, "/scenario\n3\nFix this fixture\n/retry\nA follow-up\n/result\n/exit\n")
            attempts = read(state)["tasks"][0]["attempts"]
            assert [a["status"] for a in attempts] == ["failed", "complete", "complete"]
            assert [a["revision"] for a in attempts] == [1, 1, 2]
            assert attempts[0]["prompt"] == attempts[1]["prompt"]
            assert "SIMULATED provider failure" in output
            TRANSCRIPTS["provider-retry"] = output
        checked("provider failure retries only its attempt and retains evidence", failed_provider)

        def effort_pin():
            state = folder / "effort.json"
            first, _ = run(state, "/models\n2\n/effort\n4\nKeep this explicit choice\n/exit\n")
            assert read(state)["tasks"] == [] and "cannot use high effort" in first
            second, _ = run(state, "/effort\n3\n/send\n/exit\n")
            assert read(state)["tasks"][0]["attempts"][0]["settings"]["model"] == "fixture/sprint"
            assert "SIMULATED COMPLETE" in second
        checked("unsupported explicit effort fails without losing input or changing model", effort_pin)

        def drafts():
            state = folder / "drafts.json"
            run(state, "First task\n/draft Unsent follow-up\n/models\n0\n/new\nSecond task\n/tasks\n1\n/exit\n")
            output, _ = run(state, "/send\n/exit\n")
            tasks = read(state)["tasks"]
            assert tasks[0]["attempts"][-1]["prompt"] == "Unsent follow-up"
            assert len(tasks[1]["attempts"]) == 1 and "Saved draft: Unsent follow-up" in output
        checked("draft survives picker dismissal, task navigation and restart", drafts)

        def eof_resume():
            state = folder / "eof.json"
            run(state, "/scenario\n4\nCheckpoint task\n/detach\n")
            assert read(state)["tasks"][0]["attempts"][0]["status"] == "interrupted"
            output, _ = run(state, "/resume\n/exit\n")
            attempts = read(state)["tasks"][0]["attempts"]
            assert [a["status"] for a in attempts] == ["interrupted", "complete"]
            assert "SIMULATED COMPLETE T1 attempt 2" in output
        checked("EOF interrupts foreground fixture; resume creates fresh attempt", eof_resume)

        def crash_resume():
            state = folder / "crash.json"
            session = Session(state)
            try:
                session.send("/scenario\n4\nCrash recovery input\n")
                session.until("SIMULATED running checkpoint")
                first = session.close(kill=True)
            finally:
                if session.process.poll() is None:
                    session.close(kill=True)
            assert read(state)["tasks"][0]["attempts"][0]["status"] == "running"
            second, _ = run(state, "/result\n/resume\n/exit\n")
            assert "Recovered interrupted fixture: T1" in second
            assert [a["status"] for a in read(state)["tasks"][0]["attempts"]] == ["interrupted", "complete"]
            TRANSCRIPTS["process-crash"] = first + "\n--- TERMINATED AND RESTARTED ---\n" + second
        checked("real child termination and restart reconcile accepted fixture", crash_resume)

        def cancel_followup():
            state = folder / "cancel.json"
            output, _ = run(state, "/scenario\n4\nOld goal\n/cancel\nNew goal after cancellation\n/exit\n")
            attempts = read(state)["tasks"][0]["attempts"]
            assert [a["status"] for a in attempts] == ["canceled", "complete"]
            assert attempts[1]["prompt"] == "New goal after cancellation" and attempts[1]["revision"] == 2
            assert "prior evidence and drafts preserved" in output
        checked("cancel preserves attempt; deliberate follow-up reopens task", cancel_followup)

        def save_failure():
            state = folder / "write-failure.json"
            pending = state.with_name(state.name + ".pending")
            session = Session(state)
            try:
                session.send("/scenario\n4\nSave boundary\n")
                session.until("SIMULATED running checkpoint")
                pending.mkdir()
                session.send("/finish\n")
                session.until("Cannot save fixture state")
                assert read(state)["tasks"][0]["attempts"][0]["status"] == "running"
                session.until("Task T1 | running")
                pending.rmdir()
                session.send("/finish\n")
                session.until("SIMULATED COMPLETE T1")
                output = session.close()
            finally:
                if pending.is_dir():
                    pending.rmdir()
                if session.process.poll() is None:
                    session.close(kill=True)
            assert read(state)["tasks"][0]["attempts"][0]["status"] == "complete"
            TRANSCRIPTS["write-failure"] = output
        checked("real filesystem save failure cannot claim completion; recovery succeeds", save_failure)

        def corrupt_state():
            state = folder / "corrupt.json"
            state.write_text('{"version":1,"tasks":', encoding="utf-8")
            original = state.read_bytes()
            _, error = run(state, "", expected=9)
            assert state.read_bytes() == original and "Original file preserved" in error
        checked("malformed state exits 9 without overwriting original", corrupt_state)

        def native_escape():
            state = folder / "native.json"
            output, _ = run(state, "/native\n/detach\n/exit\n")
            assert "launches no native program" in output and "Detach unavailable" in output
            assert not state.exists(), "inspection-only actions should not write empty state"
        checked("native/Detach previews do not launch or mutate anything", native_escape)

        def unicode_and_controls():
            state = folder / "unicode.json"
            output, _ = run(state, "Review 東京 café \x1b[31mtext\n/result\n/exit\n")
            assert "東京 café" in output and "\\x1b[31m" in output
            assert read(state)["tasks"][0]["attempts"][0]["prompt"].endswith("\x1b[31mtext")
        checked("Unicode retained and terminal escape sequences rendered inert", unicode_and_controls)

        def help_route():
            completed = subprocess.run([sys.executable, str(ROOT / "prototype.py"), "--unknown", "--help"],
                                       capture_output=True, text=True, encoding="utf-8", env=ENV,
                                       timeout=5, creationflags=FLAGS)
            assert completed.returncode == 0 and completed.stderr == ""
            assert "EXAMPLES" in completed.stdout and max(map(len, completed.stdout.splitlines())) <= 95
        checked("help is side-effect free and fits terminal width", help_route)

    receipt = {
        "scope": "P05 stdlib fixture subprocess acceptance; not provider or human usability evidence",
        "platform": platform.platform(), "python": sys.version.split()[0],
        "elapsed_seconds": round(time.monotonic() - started, 3),
        "checks": CHECKS, "passed": len(CHECKS), "failed": 0,
        "unexecuted": ["human/owner natural-goal usability", "Windows physical Ctrl+C key",
                       "screen reader", "macOS/Linux terminals", "real provider/account integration",
                       "bracketed multiline paste", "concurrent processes sharing one fixture state"],
        "source_hashes": {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                          for p in (ROOT / "app.py", ROOT / "prototype.py", ROOT / "check.py")},
    }
    if args.receipt:
        args.receipt.mkdir(parents=True, exist_ok=True)
        (args.receipt / "acceptance.json").write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
        for name, transcript in TRANSCRIPTS.items():
            (args.receipt / f"{name}.txt").write_text(transcript, encoding="utf-8")
    print(json.dumps({"passed": len(CHECKS), "failed": 0, "elapsed_seconds": receipt["elapsed_seconds"]}))


if __name__ == "__main__":
    main()
