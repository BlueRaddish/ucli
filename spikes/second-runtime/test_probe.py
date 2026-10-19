"""Boundary checks through real child pipes, plus semantic result/lease controls."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
import uuid

from probe import (MAX_FRAME, ProtocolError, RPC, SessionLease, command_id,
                   isolated_environment, normalize_agy_result)

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = ROOT / "testdata/second-runtime-spike/fake_msp.py"


class ProbeChecks(unittest.TestCase):
    def test_uuid7_command_identity(self):
        ids = {command_id() for _ in range(100)}
        self.assertEqual(len(ids), 100)
        self.assertTrue(all(uuid.UUID(value).version == 7 for value in ids))

    def test_cross_process_session_exclusion_and_release(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            code = ('import sys;sys.path.insert(0,sys.argv[1]);'
                    'from probe import SessionLease;from pathlib import Path;'
                    'lease=SessionLease(Path(sys.argv[2]),"fixture-scope");lease.__enter__()')
            command = [sys.executable, "-c", code, str(Path(__file__).parent), temporary]
            with SessionLease(root, "fixture-scope"):
                result = subprocess.run(command, capture_output=True, timeout=5,
                                        creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(b"FileExistsError", result.stderr)
            with SessionLease(root, "fixture-scope"):
                self.assertEqual(len(list(root.glob("*.lock"))), 1)
            self.assertEqual(list(root.glob("*.lock")), [])

    def test_environment_excludes_credentials_and_uses_private_roots(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            env = isolated_environment(root)
            self.assertNotIn("OPENAI_API_KEY", env)
            self.assertNotIn("ANTHROPIC_API_KEY", env)
            self.assertNotIn("GEMINI_API_KEY", env)
            self.assertEqual(Path(env["MUSE_AUTH_PATH"]).parent, root)
            self.assertFalse(Path(env["MUSE_AUTH_PATH"]).exists())

    def test_uncertain_exception_keeps_session_fence(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.assertRaises(TimeoutError):
                with SessionLease(root, "uncertain-scope"):
                    raise TimeoutError("synthetic unknown external outcome")
            self.assertEqual(len(list(root.glob("*.lock"))), 1)
            with self.assertRaises(FileExistsError):
                with SessionLease(root, "uncertain-scope"):
                    pass

    def test_no_read_peer_write_deadline_and_owned_descendant_cleanup(self):
        for scenario in ("no-read", "descendant"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                start = time.monotonic()
                with RPC([sys.executable, str(FIXTURE), "--scenario", scenario], root,
                         isolated_environment(root), timeout=0.75) as rpc:
                    if scenario == "no-read":
                        with self.assertRaisesRegex(TimeoutError, "write deadline"):
                            rpc.call("initialize", {"payload": "x" * 500000})
                        with self.assertRaisesRegex(ProtocolError, "poisoned"):
                            rpc.call("initialize", {})
                    else:
                        self.assertIn("marker", rpc.call("initialize", {}))
                self.assertIsNotNone(rpc.process.poll())
                self.assertTrue(all(not reader.is_alive() for reader in rpc.readers))
                self.assertLess(time.monotonic() - start, 6)

    def test_real_pipe_transport_boundaries(self):
        for scenario in ("normal", "split-utf8", "stderr-flood", "malformed", "oversize", "stall", "rpc-error"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                start = time.monotonic()
                with RPC([sys.executable, str(FIXTURE), "--scenario", scenario], root,
                         isolated_environment(root), timeout=0.75) as rpc:
                    if scenario in ("normal", "split-utf8", "stderr-flood"):
                        self.assertEqual(rpc.call("initialize", {}), {"marker": "snowman \u2603"})
                    elif scenario == "stall":
                        with self.assertRaisesRegex(TimeoutError, "unknown"):
                            rpc.call("initialize", {})
                    else:
                        with self.assertRaises(ProtocolError):
                            rpc.call("initialize", {})
                self.assertIsNotNone(rpc.process.poll())
                self.assertLess(time.monotonic() - start, 6)
                if scenario == "stderr-flood":
                    self.assertGreater(rpc.stderr_size, 65536)

    def test_outbound_frame_bound(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with RPC([sys.executable, str(FIXTURE)], root, isolated_environment(root)) as rpc:
                with self.assertRaises(ProtocolError):
                    rpc.call("initialize", {"large": "x" * MAX_FRAME})

    def test_empty_success_and_native_timeout_cannot_pass(self):
        event = {"event": "result", "result": {"status": "SUCCESS", "response": ""}}
        self.assertEqual(normalize_agy_result([event])["status"], "unknown")
        event["result"]["response"] = "partial output"
        result = normalize_agy_result([event], stderr="[agy] print timeout with turn in progress")
        self.assertEqual(result["status"], "unknown")
        self.assertEqual(normalize_agy_result([event])["status"], "complete")
        self.assertEqual(normalize_agy_result([event], process_exit=1)["status"], "failed")

    def test_schema_outputs_and_duplicate_terminal(self):
        event = {"event": "result", "result": {"status": "SUCCESS", "structured_output": False}}
        self.assertEqual(normalize_agy_result([event])["status"], "complete")
        for events in ([], [event, event]):
            with self.assertRaises(ProtocolError):
                normalize_agy_result(events)

    def test_untrusted_result_shapes_fail(self):
        for events in ([None], [{"event": "result", "result": []}]):
            with self.assertRaises(ProtocolError):
                normalize_agy_result(events)
        event = {"event": "result", "result": {"status": "SUCCESS", "response": "text", "error": "failure"}}
        self.assertEqual(normalize_agy_result([event])["status"], "failed")


if __name__ == "__main__":
    unittest.main()
