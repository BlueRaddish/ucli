"""Run from any cwd: python spikes/codex/check.py [--receipt PATH]."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import tempfile
import time
import unittest

from driver import Driver, Deadline, ProtocolError, SessionBusy, SessionLease, sanitized_account, sanitized_models

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = ROOT / "testdata/codex-spike/fake_server.py"


class Checks(unittest.TestCase):
    def setUp(self):
        scratch = Path(__file__).parent / ".scratch"
        scratch.mkdir(exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=scratch)
        self.directory = Path(self.temp.name)
        self.drivers = []

    def tearDown(self):
        for driver in self.drivers:
            driver.close()
        self.temp.cleanup()

    def driver(self, scenario="normal", turns=True):
        driver = Driver([sys.executable, str(FIXTURE), scenario], self.directory, allow_turns=turns)
        self.drivers.append(driver)
        return driver

    def lease(self, thread_id="thread_synthetic"):
        return SessionLease(self.directory / "leases", {"runtime": "codex-fixture", "connection": "fixture",
                          "workspace": "fixture", "thread": thread_id})

    def ready(self, scenario="normal"):
        driver = self.driver(scenario)
        driver.initialize()
        thread_id = driver.start_thread(self.directory)
        lease = self.lease(thread_id)
        lease.acquire()
        return driver, thread_id, lease

    def test_start_stream_and_sequential_continuation(self):
        driver, thread_id, lease = self.ready("split-utf8")
        account = sanitized_account(driver.request("account/read", {"refreshToken": False}))
        self.assertNotIn("fixture@example.invalid", json.dumps(account))
        self.assertNotIn("synthetic-identity", json.dumps(account))
        self.assertEqual(account["inference_entitlement"], "unverified")
        self.assertFalse(account["binding_verified"])
        self.assertEqual(sanitized_models(driver.request("model/list"))["count"], 1)
        driver.start_turn(thread_id, "Report the synthetic marker.", lease)
        self.assertEqual(driver.wait_terminal(), "completed")
        driver.resume(thread_id, self.directory, lease)
        driver.start_turn(thread_id, "Repeat the synthetic marker.", lease)
        self.assertEqual(driver.wait_terminal(), "completed")
        self.assertEqual(sum(e.get("method") == "item/agentMessage/delta" for e in driver.events), 2)
        lease.release_confirmed()

    def test_interrupt_is_confirmed_separately(self):
        driver, thread_id, lease = self.ready("hang")
        driver.start_turn(thread_id, "Synthetic interrupt probe.", lease)
        driver.interrupt()
        self.assertEqual(driver.wait_terminal(), "interrupted")
        self.assertTrue(driver.terminal_confirmed)
        lease.release_confirmed()

    def test_timeout_retains_uncertain_lease(self):
        driver, thread_id, lease = self.ready("interrupt-unconfirmed")
        driver.start_turn(thread_id, "Synthetic timeout probe.", lease)
        driver.interrupt()  # Acknowledgement is not terminal confirmation.
        with self.assertRaises(Deadline):
            driver.wait_terminal(timeout=.2)
        self.assertEqual(driver.external_effects, "unknown")
        with self.assertRaises(SessionBusy):
            self.lease(thread_id).acquire()

    def test_crash_after_dispatch_remains_unknown(self):
        driver, thread_id, lease = self.ready("crash")
        driver.start_turn(thread_id, "Synthetic crash probe.", lease)
        with self.assertRaises(ProtocolError):
            driver.wait_terminal(timeout=2)
        self.assertEqual(driver.external_effects, "unknown")
        self.assertTrue(lease.path.exists())

    def test_failure_is_not_success(self):
        driver, thread_id, lease = self.ready("failed")
        driver.start_turn(thread_id, "Synthetic failure.", lease)
        self.assertEqual(driver.wait_terminal(), "failed")

    def test_malformed_frames_rejected(self):
        for scenario in ("malformed", "oversize", "truncated", "wrong-id", "invalid-utf8"):
            with self.subTest(scenario=scenario):
                with self.assertRaises(ProtocolError):
                    self.driver(scenario).initialize()

    def test_stderr_flood_is_drained(self):
        driver = self.driver("stderr-flood")
        driver.initialize()
        self.assertGreater(driver.stderr_bytes, 1000000)

    def test_event_flood_fails_at_a_bound(self):
        with self.assertRaises(ProtocolError):
            self.driver("event-flood").initialize()

    def test_blocked_write_has_a_deadline(self):
        driver = self.driver("no-read")
        driver.initialize()
        started = time.monotonic()
        with self.assertRaises(Deadline):
            driver.request("turn/start", {"threadId": "synthetic", "input": [{"type": "text", "text": "x" * 50000}]})
        self.assertLess(time.monotonic() - started, 4)
        self.assertEqual(driver.external_effects, "unknown")
        with self.assertRaisesRegex(ProtocolError, "must be closed"):
            driver.request("account/read", {})

    def test_approval_denied_without_tool_execution(self):
        driver, thread_id, lease = self.ready("approval")
        driver.start_turn(thread_id, "Synthetic request for forbidden tool.", lease)
        self.assertEqual(driver.wait_terminal(), "completed")

    def test_unknown_server_request_rejected(self):
        driver, thread_id, lease = self.ready("unknown-request")
        driver.start_turn(thread_id, "Synthetic request.", lease)
        self.assertEqual(driver.wait_terminal(), "completed")

    def test_account_and_trust_gate_prevents_turn_dispatch(self):
        driver = self.driver(turns=False)
        driver.initialize()
        with self.assertRaisesRegex(ProtocolError, "live inference blocked"):
            driver.request("turn/start", {"threadId": "synthetic", "input": []})
        self.assertNotIn("turn/start", driver.sent_methods)
        self.assertFalse(driver.dispatched)

    def test_matching_session_lease_required(self):
        driver, thread_id, lease = self.ready()
        other = self.lease("other-synthetic-thread")
        other.acquire()
        with self.assertRaises(SessionBusy):
            driver.resume(thread_id, self.directory, other)
        with self.assertRaises(SessionBusy):
            driver.start_turn(thread_id, "Synthetic input", other)
        driver.start_turn(thread_id, "Synthetic input", lease)
        with self.assertRaises(SessionBusy):
            driver.resume(thread_id, self.directory, lease)
        with self.assertRaises(SessionBusy):
            driver.start_turn(thread_id, "Competing synthetic input", lease)

    def test_session_exclusion_cross_process(self):
        lease = self.lease()
        lease.acquire()
        code = "from pathlib import Path; from driver import SessionLease, SessionBusy; import sys; "
        code += "x=SessionLease(Path(sys.argv[1]), {\"runtime\":\"codex-fixture\",\"connection\":\"fixture\",\"workspace\":\"fixture\",\"thread\":\"thread_synthetic\"});\n"
        code += "try: x.acquire()\nexcept SessionBusy: sys.exit(17)\nsys.exit(1)"
        result = subprocess.run([sys.executable, "-c", code, str(self.directory / "leases")],
                                cwd=Path(__file__).parent, capture_output=True, timeout=5,
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        self.assertEqual(result.returncode, 17)
        lease.release_confirmed()
        self.lease().acquire()

    def test_owned_descendant_stops_on_close(self):
        driver = self.driver()
        driver.initialize()
        heartbeat = self.directory / "heartbeat"
        driver.request("fixture/child", {"heartbeat": str(heartbeat)})
        deadline = time.monotonic() + 3
        while not heartbeat.exists() and time.monotonic() < deadline:
            time.sleep(.02)
        self.assertTrue(heartbeat.exists())
        driver.close()
        self.drivers.remove(driver)
        time.sleep(.1)
        previous = heartbeat.read_bytes()
        time.sleep(.15)
        self.assertEqual(heartbeat.read_bytes(), previous)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--receipt", type=Path)
    args = parser.parse_args()
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(Checks))
    receipt = {"scope": "synthetic fixture consuming driver; no inference", "schema_version": "codex-cli 0.162.1",
               "date_utc": datetime.now(timezone.utc).isoformat(),
               "contract_revision": "spike-v0-2026-10-18", "os": platform.system(), "architecture": platform.machine(),
               "python": platform.python_version(), "tests_run": result.testsRun,
               "checks": unittest.defaultTestLoader.getTestCaseNames(Checks),
               "failures": len(result.failures), "errors": len(result.errors), "skipped": len(result.skipped),
               "exit_code": 0 if result.wasSuccessful() else 1}
    receipt["source_sha256"] = {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest() for name in
                                ("spikes/codex/driver.py", "spikes/codex/check.py", "testdata/codex-spike/fake_server.py", "testdata/codex-spike/protocol-0.162.1.json")}
    if args.receipt:
        args.receipt.parent.mkdir(parents=True, exist_ok=True)
        args.receipt.write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
    return receipt["exit_code"]


if __name__ == "__main__":
    raise SystemExit(main())
