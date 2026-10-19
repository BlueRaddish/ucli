"""Drive only the packaged synthetic P01 binary; standard library, no provider calls."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import queue
import random
import shutil
import subprocess
import sys
import tempfile
import threading
import time

FLAGS = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0


def spawn(args, **kwargs):
    return subprocess.Popen([str(a) for a in args], creationflags=FLAGS, **kwargs)


def run(args, timeout=10, expected=0):
    with spawn(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE) as p:
        try:
            out, err = p.communicate(timeout=timeout)
        except BaseException:
            p.kill()
            p.communicate(timeout=2)
            raise
        assert expected is None or p.returncode == expected, (args, p.returncode, out[-1000:], err[-1000:])
        return p.returncode, out.decode("utf-8", errors="replace"), err.decode("utf-8", errors="replace")


def objects(text):
    return [json.loads(line) for line in text.splitlines() if line]


def config(root):
    root.mkdir(parents=True, exist_ok=True)
    (root / "config.json").write_text('{"marker":"synthetic-p01"}', encoding="utf-8")
    return root


def await_path(path, timeout=5):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path.exists():
            return
        time.sleep(.01)
    raise AssertionError(f"fixture path never appeared: {path}")


def await_stopped(binary, pid, timeout=3):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = json.loads(run([binary, "probe", "--pid", pid])[1])
        if not value["alive"]:
            return
        time.sleep(.02)
    raise AssertionError(f"owned fixture descendant {pid} still alive")


def percentiles(values):
    values = sorted(values)
    return {"n": len(values), "p50_ms": values[math.ceil(len(values)*.5)-1],
            "p95_ms": values[math.ceil(len(values)*.95)-1], "max_ms": values[-1]}


class Lines:
    """The deadline covers reading stdout, including a live child with no EOF."""
    def __init__(self, stream):
        self.items = queue.Queue()
        def read():
            try:
                for line in stream:
                    self.items.put(line)
            finally:
                self.items.put(None)
        self.thread = threading.Thread(target=read, daemon=True)
        self.thread.start()

    def next(self, deadline):
        try:
            return self.items.get(timeout=max(0, deadline-time.monotonic()))
        except queue.Empty as exc:
            raise TimeoutError("fixture stdout deadline exceeded") from exc


def measure(args, deadline_s=10):
    start = time.perf_counter_ns()
    first = None
    deadline = time.monotonic() + deadline_s
    # stderr is drained by the OS into a temporary file; no pipe-fill deadlock.
    with tempfile.TemporaryFile() as errfile, spawn(args, stdout=subprocess.PIPE, stderr=errfile,
                                                   text=True, encoding="utf-8") as p:
        lines = Lines(p.stdout)
        try:
            rows = []
            while (line := lines.next(deadline)) is not None:
                item = json.loads(line)
                rows.append(item)
                if first is None and item.get("type") == "message_delta":
                    first = (time.perf_counter_ns() - start) / 1e6
            p.wait(timeout=max(.001, deadline-time.monotonic()))
            errfile.seek(0)
            assert p.returncode == 0, (p.returncode, errfile.read(1000))
            assert rows and first is not None, "missing benchmark content"
        finally:
            if p.poll() is None:
                p.kill()
            p.wait(timeout=2)
            lines.thread.join(timeout=2)
            assert not lines.thread.is_alive(), "fixture reader did not terminate"
    return {"elapsed_ms": (time.perf_counter_ns()-start)/1e6, "first_content_ms": first, "result": rows[-1]}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--cold", type=int, default=30)
    parser.add_argument("--warm", type=int, default=100)
    args = parser.parse_args()
    if args.cold < 1 or args.warm < 1:
        parser.error("--cold and --warm must be positive")
    binary = args.binary.resolve()
    args.evidence.mkdir(parents=True, exist_ok=True)
    receipt = {"platform": platform.platform(), "python": platform.python_version(),
               "command": [sys.executable, *sys.argv],
               "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
               "cpu_count": os.cpu_count(), "processor": platform.processor(),
               "terminal": "hidden pipes; no interactive terminal exercised",
               "workload": "shared development machine; no CPU affinity/cache clearing/security-software changes",
               "fixture_source_sha256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                   for p in sorted(Path(__file__).parent.iterdir()) if p.suffix in (".go", ".py") or p.name in ("go.mod", "go.sum")},
               "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
               "cold_definition": "new process, OS filesystem cache NOT cleared",
               "warm_definition": "persistent fixture-supervisor service; fresh provider child per request",
               "checks": [], "samples": [], "failures": [], "skipped": []}

    def check(name, action):
        start = time.monotonic()
        try:
            detail = action()
            receipt["checks"].append({"name": name, "status": "passed", "detail": detail,
                                      "seconds": time.monotonic()-start})
            print(f"PASS {name}", flush=True)
        except Exception as exc:
            receipt["failures"].append({"name": name, "error": repr(exc)})
            print(f"FAIL {name}: {exc!r}", flush=True)

    with tempfile.TemporaryDirectory(prefix="ucli-p01-") as scratch:
        scratch = Path(scratch)
        root = config(scratch / "state")

        def execute(scenario, expected=0, timeout=3000, delay=0):
            rid = f"{scenario}-{time.time_ns()}"
            rc, out, err = run([binary, "run", "--root", root, "--id", rid,
                                "--scenario", scenario, "--timeout-ms", timeout, "--delay-ms", delay], expected=expected)
            return rid, objects(out), err

        def happy():
            rid, rows, _ = execute("quick")
            assert rows[0]["type"] == "accepted" and rows[-1]["status"] == "complete"
            db = json.loads(run([binary, "inspect", "--root", root])[1])
            assert db["integrity"] == "ok" and db["states"][rid] == "complete"
            return {"sqlite": db["sqlite"], "integrity": db["integrity"], "event_types": [r["type"] for r in rows]}
        check("packaged-command-config-sqlite-stream", happy)

        def split():
            _, rows, _ = execute("split")
            assert next(r["text"] for r in rows if r["type"] == "message_delta") == "한글 ü 👋"
            return "byte-split UTF-8 and JSON frame preserved"
        check("split-utf8-frame", split)

        def flood():
            _, rows, _ = execute("stderr-flood")
            assert rows[-1]["stderr_bytes"] == 4*1024*1024 and rows[-1]["status"] == "complete"
            return {"stderr_bytes": rows[-1]["stderr_bytes"]}
        check("stderr-flood-drained", flood)

        for scenario in ("malformed", "oversized", "fail", "trailing"):
            def bad(scenario=scenario):
                _, rows, err = execute(scenario, expected=1)
                assert rows[-1]["status"] == "failed" and rows[-1].get("error")
                return {"expected_error": rows[-1]["error"]}
            check(f"reject-{scenario}", bad)

        def persistence():
            rid, _, _ = execute("crash-after-accept", expected=7)
            state = json.loads(run([binary, "inspect", "--root", root])[1])
            assert state["states"][rid] == "accepted" and state["integrity"] == "ok"
            return "committed intent survives process death; no invented completion"
        check("crash-after-accepted-transaction", persistence)

        for scenario in ("descendant", "ignore-cancel", "approval"):
            def stop(scenario=scenario):
                (root / "leaf.pid").unlink(missing_ok=True)
                _, rows, _ = execute(scenario, timeout=350)
                result = rows[-1]
                assert result["status"] == "interrupted" and result["cancel_ms"] < 2000
                if scenario != "approval":
                    await_path(root / "leaf.pid")
                    pid = int((root / "leaf.pid").read_text())
                    await_stopped(binary, pid)
                return {"cancel_ms": result["cancel_ms"], "status": result["status"]}
            check(f"cancel-{scenario}", stop)

        def broken_cleanup():
            leakroot = config(scratch / "negative-cleanup")
            p = spawn([binary, "child", "--root", leakroot, "--scenario", "descendant"],
                      stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            pid = None
            try:
                await_path(leakroot / "leaf.pid")
                await_path(leakroot / "heartbeat")
                pid = int((leakroot / "leaf.pid").read_text())
                p.kill()  # Deliberately wrong: parent-only termination.
                p.wait(timeout=5)
                try:
                    await_stopped(binary, pid, timeout=.2)
                except AssertionError:
                    return "checker rejected deliberately surviving descendant; fixture stop file cleaned it"
                raise AssertionError("negative cleanup control was not detected")
            finally:
                (leakroot / "stop-leaf").write_text("synthetic-stop")
                if p.poll() is None:
                    p.kill(); p.wait(timeout=5)
                if pid is not None:
                    await_stopped(binary, pid)
        check("negative-parent-only-cleanup-detector", broken_cleanup)

        def identity_and_leases():
            git = shutil.which("git")
            assert git, "git is needed for actual linked-worktree fixture"
            repo = scratch / "repo space ü"
            run([git, "init", "--quiet", repo])
            run([git, "-C", repo, "-c", "user.name=P01 Fixture", "-c", "user.email=p01@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "synthetic fixture"])
            (repo / "src").mkdir()
            other = scratch / "linked-worktree"
            run([git, "-C", repo, "worktree", "add", "--quiet", "-b", "fixture-other", other])
            alias = scratch / "alias"
            if os.name == "nt":
                # Only fixed synthetic paths without shell metacharacters are passed.
                run(["cmd.exe", "/c", "mklink", "/J", alias, repo])
            else:
                alias.symlink_to(repo, target_is_directory=True)
            identity = lambda path: json.loads(run([binary, "identity", "--root", path])[1])
            a, b, c, d = [identity(p) for p in (repo, repo / "src", alias, other)]
            assert a["id"] == b["id"] == c["id"] and a["id"] != d["id"]
            assert b["cwd"] != b["root"]
            def lease(mode, workspace, hold=400):
                return [binary, "lease", "--root", root, "--workspace", workspace,
                        "--mode", mode, "--hold-ms", str(hold)]
            for mode in ("write", "read"):
                with spawn(lease(mode, repo, 1500), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8") as holder:
                    assert json.loads(Lines(holder.stdout).next(time.monotonic()+5))["type"] == "lease_acquired"
                    code, _, err = run(lease("write", alias, 10), expected=1)
                    assert "workspace contention" in err
                    run(lease("write", other, 10))
                    if mode == "read":
                        run(lease("read", alias, 10))
                    holder.communicate(timeout=5)
                    assert holder.returncode == 0
            return {"alias_matches": True, "subdirectory_matches": True, "linked_worktree_distinct": True,
                    "writer_exclusion": True, "shared_readers": True, "cwd_preserved": True}
        check("workspace-alias-worktree-cross-process-leases", identity_and_leases)

        def timing_negative():
            normal = measure([binary, "child", "--root", root, "--scenario", "quick"])
            delayed = measure([binary, "child", "--root", root, "--scenario", "quick", "--delay-ms", "200"])
            delta = delayed["first_content_ms"]-normal["first_content_ms"]
            assert delta > 150, delta
            return {"injected_ms": 200, "detected_delta_ms": delta}
        check("negative-injected-delay-detector", timing_negative)

        def stalled_stdout():
            start = time.monotonic()
            try:
                measure([binary, "child", "--root", root, "--scenario", "stall"], deadline_s=.2)
            except TimeoutError:
                elapsed = time.monotonic()-start
                assert elapsed < 2, elapsed
                return {"deadline_ms": 200, "bounded_elapsed_ms": elapsed*1000}
            raise AssertionError("live stdout stall bypassed deadline")
        check("negative-live-stdout-stall-deadline", stalled_stdout)

        def benchmarks():
            randomizer = random.Random(1)
            for i in range(args.cold):
                pair = [("direct", [binary, "child", "--root", root, "--scenario", "quick"]),
                        ("foreground", [binary, "run", "--root", root, "--id", f"cold-{i}", "--scenario", "quick"])]
                randomizer.shuffle(pair)
                values = {name: measure(cmd) for name, cmd in pair}
                receipt["samples"].append({"kind": "cold", "index": i, **values,
                    "overhead_ms": values["foreground"]["first_content_ms"]-values["direct"]["first_content_ms"]})
            host = spawn([binary, "host", "--root", root], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=subprocess.PIPE, text=True, encoding="utf-8", bufsize=1)
            try:
                host_lines = Lines(host.stdout)
                assert json.loads(host_lines.next(time.monotonic()+5))["type"] == "host_ready"
                for i in range(args.warm):
                    values = {}
                    order = ["direct", "supervisor"]
                    randomizer.shuffle(order)
                    for name in order:
                        if name == "direct":
                            values[name] = measure([binary, "child", "--root", root, "--scenario", "quick"])
                        else:
                            start = time.perf_counter_ns(); first = None
                            host.stdin.write(json.dumps({"id": f"warm-{i}", "scenario": "quick", "timeout_ms": 3000})+"\n")
                            host.stdin.flush()
                            deadline = time.monotonic()+5
                            while True:
                                row = json.loads(host_lines.next(deadline))
                                if row["type"] == "message_delta" and first is None:
                                    first = (time.perf_counter_ns()-start)/1e6
                                if row["type"] == "result":
                                    assert row["status"] == "complete" and row["host"] == "fixture-supervisor"
                                    values[name] = {"elapsed_ms": (time.perf_counter_ns()-start)/1e6, "first_content_ms": first, "result": row}
                                    break
                    receipt["samples"].append({"kind": "warm", "index": i, **values,
                        "overhead_ms": values["supervisor"]["first_content_ms"]-values["direct"]["first_content_ms"]})
                host.stdin.close();host.wait(timeout=5)
                assert host.returncode == 0
            finally:
                if host.poll() is None:
                    host.kill()
                host.wait(timeout=2)
                host.stdout.close()
                host.stderr.close()
            summaries = {kind: percentiles([s["overhead_ms"] for s in receipt["samples"] if s["kind"] == kind]) for kind in ("cold", "warm")}
            receipt["benchmark_summary"] = summaries
            # These are fixture-local overhead gates, not product/terminal SLO certification.
            assert summaries["cold"]["p95_ms"] <= 150, summaries
            assert summaries["warm"]["p95_ms"] <= 75, summaries
            return summaries
        check("native-relative-overhead-budgets", benchmarks)

        receipt["skipped"] = ["real provider/authentication/credential-store integration", "interactive terminal rendering/resize and keyboard UX",
                              "production supervisor authenticated IPC/detach", "schema migration maintenance barrier (P08)",
                              "stale workspace-owner recovery (P08)", "full long-stream memory and 1/4/8 concurrency matrix"]
    receipt["cleanup"] = "synthetic temporary workspace removed; receipts remain outside it"
    receipt["exit_code"] = 1 if receipt["failures"] else 0
    receipt["finished_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    (args.evidence / "receipt.json").write_text(json.dumps(receipt, indent=2, ensure_ascii=False), encoding="utf-8")
    print(json.dumps({"passed":len(receipt["checks"]),"failed":len(receipt["failures"]),"evidence":str(args.evidence)}, ensure_ascii=False), flush=True)
    raise SystemExit(1 if receipt["failures"] else 0)


if __name__ == "__main__":
    main()
