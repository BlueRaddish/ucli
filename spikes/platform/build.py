"""Build/test the isolated spike and retain commands, exits, and binary identities."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import sqlite3
import subprocess
import sys
import time

p = argparse.ArgumentParser()
p.add_argument("--go", type=Path, required=True)
p.add_argument("--evidence", type=Path, required=True)
args = p.parse_args()
module = Path(__file__).resolve().parent
evidence = args.evidence.resolve()
evidence.mkdir(parents=True, exist_ok=True)
output = module / "_build"
output.mkdir(exist_ok=True)
env = os.environ.copy()
env.update(GOTOOLCHAIN="local", CGO_ENABLED="0")
if os.name == "nt":
    dev = Path.home() / ".local/share/ucli-dev"
    env.update(GOMODCACHE=str(dev / "cache/gomod"), GOCACHE=str(dev / "cache/gobuild"))
flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
records = []


def command(label, command, changes=None):
    child_env = env | (changes or {})
    start = time.monotonic()
    proc = subprocess.run([str(a) for a in command], cwd=module, env=child_env,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          creationflags=flags, timeout=600)
    (evidence / f"{label}.stdout.txt").write_bytes(proc.stdout)
    (evidence / f"{label}.stderr.txt").write_bytes(proc.stderr)
    records.append({"label": label, "command": [str(a) for a in command],
                    "cwd": str(module), "environment_overrides": changes or {},
                    "exit_code": proc.returncode, "seconds": time.monotonic()-start})
    print(label, "exit", proc.returncode, flush=True)
    if proc.returncode:
        raise RuntimeError(proc.stderr.decode("utf-8", errors="replace"))
    return proc.stdout.decode("utf-8", errors="replace")


try:
    command("go-version", [args.go, "version"])
    command("go-module-verify", [args.go, "mod", "verify"])
    command("go-test", [args.go, "test", "-count=1", "-v", "./..."])
    command("go-vet", [args.go, "vet", "./..."])
    command("go-dependencies", [args.go, "list", "-m", "-json", "all"])
    binaries = []
    for target_os, target_arch in [("windows", "amd64"), ("linux", "amd64"), ("darwin", "arm64")]:
        binary = output / f"p01-{target_os}-{target_arch}{'.exe' if target_os == 'windows' else ''}"
        command(f"build-{target_os}-{target_arch}", [args.go, "build", "-trimpath", "-o", binary, "."],
                {"GOOS": target_os, "GOARCH": target_arch})
        binaries.append({"os": target_os, "arch": target_arch, "path": str(binary),
                         "bytes": binary.stat().st_size,
                         "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                         "status": "cross-compiled; execution recorded separately"})
    alternatives = {"python": sys.version, "python_stdlib_sqlite": sqlite3.sqlite_version,
                    "rustc_path": shutil.which("rustc"), "cargo_path": shutil.which("cargo"),
                    "node_path": shutil.which("node"), "comparison_scope": "inventory only; no comparative product benchmark"}
    if alternatives["node_path"]:
        alternatives["node_version"] = command("node-version", [alternatives["node_path"], "--version"]).strip()
    (evidence / "build-receipt.json").write_text(json.dumps({"host": platform.platform(), "cpu_count": os.cpu_count(),
        "source_sha256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(module.iterdir())
                          if p.suffix in (".go", ".py") or p.name in ("go.mod", "go.sum")},
        "commands": records, "binaries": binaries, "alternative_inventory": alternatives,
        "environment": {key:env[key] for key in ("GOTOOLCHAIN", "CGO_ENABLED", "GOMODCACHE", "GOCACHE") if key in env}}, indent=2), encoding="utf-8")
finally:
    (evidence / "commands.json").write_text(json.dumps(records, indent=2), encoding="utf-8")
