"""Exercise P04 account boundaries through owned child processes and real SQLite."""

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
REPO = ROOT.parent.parent
FIXTURE = REPO / "testdata/account-isolation/fixture.json"
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
ENV = dict(os.environ, PYTHONIOENCODING="utf-8", PYTHONUTF8="1")
RAW = []
CHECKS = []


def argv(db, *args):
    return [sys.executable, str(ROOT / "experiment.py"), "--db", str(db), *map(str, args)]


def record(command, code, output, error=""):
    receipts = [json.loads(line) for line in output.splitlines() if line.strip()]
    assert all(r.get("fixture_only") is True for r in receipts)
    assert "SYNTHETIC-ONLY-" not in output + error, "raw fixture credential value was logged"
    RAW.append({"command": command, "exit_code": code, "stdout": receipts, "stderr": error})
    return receipts


def run(db, *args, expected=0, input_text=""):
    command = argv(db, *args)
    result = subprocess.run(command, input=input_text, capture_output=True, text=True,
                            encoding="utf-8", timeout=8, creationflags=FLAGS, env=ENV)
    receipt = record(command, result.returncode, result.stdout, result.stderr)
    assert result.returncode == expected, (command, result.returncode, receipt, result.stderr)
    assert result.stderr == "", result.stderr
    return receipt[-1]


class Worker:
    def __init__(self, db, *args):
        self.command = argv(db, *args)
        self.process = subprocess.Popen(self.command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=subprocess.PIPE, text=True, encoding="utf-8",
                                        creationflags=FLAGS, env=ENV)
        self.first = ""

    def acquired(self):
        messages = queue.Queue()
        thread = threading.Thread(target=lambda: messages.put(self.process.stdout.readline()), daemon=True)
        thread.start()
        try:
            self.first = messages.get(timeout=5)
        except queue.Empty as exc:
            self.process.kill()
            raise AssertionError("refresh child did not acknowledge its lease") from exc
        thread.join(timeout=1)
        receipt = json.loads(self.first)
        assert receipt.get("event") == "lease-acquired", receipt
        return receipt

    def commit(self, expected=0):
        output, error = self.process.communicate("commit\n", timeout=5)
        receipts = record(self.command, self.process.returncode, self.first + output, error)
        assert self.process.returncode == expected, receipts
        assert error == ""
        return receipts[-1]

    def terminate(self):
        self.process.terminate()
        output, error = self.process.communicate(timeout=5)
        record(self.command, self.process.returncode, self.first + output, error)


def parallel(db, requests):
    children = [(argv(db, *request), subprocess.Popen(argv(db, *request), stdout=subprocess.PIPE,
                stderr=subprocess.PIPE, text=True, encoding="utf-8", creationflags=FLAGS, env=ENV))
                for request in requests]
    results = []
    for command, child in children:
        output, error = child.communicate(timeout=8)
        results.append((child.returncode, record(command, child.returncode, output, error)[-1]))
        assert error == ""
    return results


def binding_guard(receipt):
    assert receipt.get("dispatched") is True
    assert receipt["observed"] == receipt["selected"], "wrong identity dispatched"


def current_value(snapshot, owner):
    return next(item for item in snapshot["owners"] if item["owner"] == owner)["value_sha256"]


def digest(tag):
    return hashlib.sha256(("SYNTHETIC-ONLY-refresh-" + tag).encode()).hexdigest()


def negative_control(name, check):
    try:
        check()
    except AssertionError:
        CHECKS.append({"name": name, "result": "expected failure detected"})
        print("PASS negative control: " + name, flush=True)
        return
    raise AssertionError("checker accepted deliberately broken behavior: " + name)


def checked(name):
    CHECKS.append({"name": name, "result": "pass"})
    print("PASS " + name, flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--receipt", type=Path)
    args = parser.parse_args()
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="ucli-p04-synthetic-") as scratch:
        directory = Path(scratch)
        db, profiles = directory / "accounts.sqlite", directory / "profiles"
        run(db, "init", "--fixture", FIXTURE, "--profiles", profiles)
        rejected = run(db, "probe", "--connection", "connection-a", "--profile", profiles / "home-a",
                       "--backend", "shared-global", expected=3)
        assert rejected["dispatched"] is False and rejected["selected"] != rejected["observed"]
        assert run(db, "inspect")["dispatches"] == []
        checked("separate fake HOME/profile roots do not hide shared-global identity mismatch")

        isolated = parallel(db, [
            ("probe", "--connection", "connection-a", "--profile", profiles / "home-a", "--backend", "isolated-owner"),
            ("probe", "--connection", "connection-b", "--profile", profiles / "home-b", "--backend", "isolated-owner"),
        ])
        assert [code for code, _ in isolated] == [0, 0]
        for _, receipt in isolated:
            binding_guard(receipt)
        assert len(run(db, "inspect")["dispatches"]) == 2
        checked("two consuming children bind independent opaque owners before dispatch")

        unsafe = run(db, "unsafe-probe", "--connection", "connection-a", "--profile", profiles / "home-a",
                     "--backend", "shared-global")
        negative_control("binding checker rejects intentionally unguarded shared backend", lambda: binding_guard(unsafe))

        first = Worker(db, "refresh-worker", "--owner", "owner-fixture-a", "--start-now", 0,
                       "--finish-now", 12, "--tag", "stale-a")
        first.acquired()
        busy = run(db, "refresh-worker", "--owner", "owner-fixture-a", "--start-now", 1,
                   "--finish-now", 2, "--tag", "blocked", expected=4, input_text="commit\n")
        assert busy["reason"] == "refresh already owned"
        other = Worker(db, "refresh-worker", "--owner", "owner-fixture-b", "--start-now", 1,
                       "--finish-now", 2, "--tag", "fresh-b")
        other.acquired()
        assert other.commit()["generation"] == 2
        checked("cross-process refresh lease serializes one owner without blocking another")

        successor = Worker(db, "refresh-worker", "--owner", "owner-fixture-a", "--start-now", 11,
                           "--finish-now", 12, "--tag", "fresh-a")
        assert successor.acquired()["epoch"] == 2
        assert successor.commit()["generation"] == 2
        stale = first.commit(expected=3)
        assert stale["reason"] == "stale refresh rejected"
        snap = run(db, "inspect")
        assert current_value(snap, "owner-fixture-a") == digest("fresh-a")
        assert current_value(snap, "owner-fixture-b") == digest("fresh-b")
        checked("late stale refresh cannot overwrite successor generation or another owner")

        killed = Worker(db, "refresh-worker", "--owner", "owner-fixture-a", "--start-now", 20,
                        "--finish-now", 21, "--tag", "never-committed")
        killed.acquired()
        killed.terminate()
        run(db, "refresh-worker", "--owner", "owner-fixture-a", "--start-now", 21,
            "--finish-now", 22, "--tag", "too-soon", expected=4, input_text="commit\n")
        replacement = run(db, "refresh-worker", "--owner", "owner-fixture-a", "--start-now", 31,
                          "--finish-now", 32, "--tag", "after-crash", input_text="commit\n")
        assert replacement["generation"] == 3
        checked("terminated refresh child leaves bounded ownership; later lease can recover")

        run(db, "unsafe-refresh", "--owner", "owner-fixture-a", "--tag", "unsafe-stale")
        corrupted = run(db, "inspect")
        def current_generation_guard():
            assert current_value(corrupted, "owner-fixture-a") == digest("after-crash")
        negative_control("generation checker detects intentionally unfenced overwrite", current_generation_guard)

        known = parallel(db, [
            ("reserve", "--connection", "connection-a", "--attempt", "known-a", "--now", 0),
            ("reserve", "--connection", "connection-b", "--attempt", "known-b", "--now", 0),
        ])
        assert sorted(code for code, _ in known) == [0, 4]
        winner = next(receipt for code, receipt in known if code == 0)["attempt"]
        connection = "connection-a" if winner == "known-a" else "connection-b"
        loser_connection = "connection-b" if connection == "connection-a" else "connection-a"
        checked("different connections sharing one known pool cannot duplicate capacity")

        duplicate = run(db, "reserve", "--connection", connection, "--attempt", winner, "--now", 1)
        assert duplicate["duplicate"] is True
        run(db, "reserve", "--connection", loser_connection, "--attempt", winner, "--now", 1, expected=2)
        assert len(run(db, "inspect")["reservations"]) == 1
        checked("duplicate reservation is idempotent and changed intent is rejected")

        unknown = parallel(db, [
            ("reserve", "--connection", "unknown-a", "--attempt", "unknown-a", "--now", 0),
            ("reserve", "--connection", "unknown-b", "--attempt", "unknown-b", "--now", 0),
        ])
        assert sorted(code for code, _ in unknown) == [0, 4]
        unknown_winner = next(receipt for code, receipt in unknown if code == 0)["attempt"]
        checked("unknown shared pool admits at most one in-flight task across connections")

        run(db, "reserve", "--connection", loser_connection, "--attempt", "after-expiry", "--now", 11, expected=4)
        run(db, "reserve", "--connection", "unknown-a", "--attempt", "unknown-after-expiry", "--now", 11, expected=4)
        run(db, "reconcile", "--attempt", winner, "--proof", "cancel-request")
        run(db, "reserve", "--connection", loser_connection, "--attempt", "after-cancel", "--now", 12, expected=4)
        active = run(db, "inspect")["reservations"]
        assert len(active) == 2 and all(item["status"] == "uncertain" for item in active)
        checked("lease expiry and cancel preserve uncertain quota reservations")

        run(db, "reconcile", "--attempt", winner, "--proof", "confirmed-stopped", expected=5)
        run(db, "reconcile", "--attempt", winner, "--proof", "confirmed-stopped", "--remaining", 8)
        run(db, "reserve", "--connection", loser_connection, "--attempt", "after-proof", "--now", 13)
        run(db, "reconcile", "--attempt", unknown_winner, "--proof", "confirmed-unsent")
        run(db, "reserve", "--connection", "unknown-b", "--attempt", "unknown-after-proof", "--now", 13)
        checked("capacity reopens only after outcome reconciliation; known units require fresh observation")
        snapshot = run(db, "inspect")

    receipt = {
        "scope": "P04 synthetic process/SQLite experiment only; real provider identity isolation is pending",
        "platform": platform.platform(), "python": sys.version.split()[0],
        "sqlite_version": snapshot["sqlite_version"], "journal_mode": snapshot["journal_mode"],
        "passed": len(CHECKS), "failed": 0, "negative_controls": 2,
        "checks": CHECKS, "elapsed_seconds": round(time.monotonic() - started, 3),
        "unexecuted": ["real provider two-account identity/refresh isolation", "OS keychain/service isolation",
                       "provider quota API and source attribution", "macOS/Linux execution", "production account manager"],
        "source_hashes": {str(p.relative_to(REPO)).replace('\\', '/'): hashlib.sha256(p.read_bytes()).hexdigest()
                          for p in (ROOT / "experiment.py", ROOT / "check.py", FIXTURE)},
    }
    if args.receipt:
        args.receipt.mkdir(parents=True, exist_ok=True)
        (args.receipt / "acceptance.json").write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
        (args.receipt / "commands.jsonl").write_text("".join(json.dumps(row) + "\n" for row in RAW), encoding="utf-8")
        (args.receipt / "final-snapshot.json").write_text(json.dumps(snapshot, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"passed": len(CHECKS), "failed": 0, "negative_controls": 2}))


if __name__ == "__main__":
    main()
