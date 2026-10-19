"""Run the packaged Linux binary on WSL's native temporary filesystem."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import tempfile

p = argparse.ArgumentParser()
p.add_argument("--evidence", type=Path, required=True)
p.add_argument("--distribution", default="Ubuntu")
p.add_argument("--native", action="store_true", help=argparse.SUPPRESS)
args = p.parse_args()
module = Path(__file__).resolve().parent
evidence = args.evidence.resolve()
evidence.mkdir(parents=True, exist_ok=True)
if args.native:
    if os.name == "nt":
        p.error("--native requires the Linux host")
    with tempfile.TemporaryDirectory(prefix="ucli-p01-package-") as root:
        binary = Path(root) / "p01-linux-amd64"
        shutil.copy2(module / "_build/p01-linux-amd64", binary)
        binary.chmod(0o700)
        result = subprocess.run([sys.executable, module / "verify.py", "--binary", binary,
                                 "--evidence", evidence], timeout=150)
        raise SystemExit(result.returncode)

def mounted(path):
    path = path.resolve().as_posix()
    if len(path) < 3 or path[1:3] != ":/":
        raise ValueError("this helper requires a Windows drive path")
    return "/mnt/" + path[0].lower() + path[2:]

# No shell expansion or Python source transported through a command string.
command = ["wsl.exe", "-d", args.distribution, "--exec", "python3", mounted(Path(__file__)),
           "--native", "--evidence", mounted(evidence)]
start = time.monotonic()
proc = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                      creationflags=subprocess.CREATE_NO_WINDOW, timeout=180)
(evidence/"runner.stdout.txt").write_bytes(proc.stdout)
(evidence/"runner.stderr.txt").write_bytes(proc.stderr)
(evidence/"host-launch.json").write_text(json.dumps({"command": command, "exit_code": proc.returncode,
    "seconds": time.monotonic()-start, "executable_storage": "copy on native WSL /tmp; removed after run",
    "source_binary": str(module/"_build/p01-linux-amd64")}, indent=2), encoding="utf-8")
sys.stdout.buffer.write(proc.stdout)
sys.stderr.buffer.write(proc.stderr)
raise SystemExit(proc.returncode)
