"""Synthetic wire fixture; never calls a model or touches credentials."""
import argparse
import json
import sys
import subprocess
import time

parser = argparse.ArgumentParser()
parser.add_argument("--scenario", default="normal")
args = parser.parse_args()
if args.scenario == "no-read":
    time.sleep(30)
    raise SystemExit(0)


def emit(value):
    raw = (json.dumps(value, ensure_ascii=False) + "\n").encode("utf-8")
    if args.scenario == "split-utf8":
        for byte in raw:
            sys.stdout.buffer.write(bytes([byte]))
        sys.stdout.buffer.flush()
    else:
        sys.stdout.buffer.write(raw)
        sys.stdout.buffer.flush()


for line in sys.stdin:
    message = json.loads(line)
    if "id" not in message:
        continue
    if args.scenario == "malformed":
        print("{broken", flush=True)
        break
    if args.scenario == "oversize":
        print("x" * (1024 * 1024 + 1), flush=True)
        break
    if args.scenario == "stall":
        time.sleep(30)
        break
    if args.scenario == "stderr-flood":
        sys.stderr.buffer.write(b"diagnostic\n" * 20000)
        sys.stderr.buffer.flush()
    if args.scenario == "descendant":
        # Spawn only after the host has joined its owned process job/group.
        subprocess.Popen([sys.executable, "-c", "import time;time.sleep(30)"],
                         stdin=subprocess.DEVNULL, stdout=sys.stdout, stderr=sys.stderr,
                         creationflags=subprocess.CREATE_NO_WINDOW if sys.platform == "win32" else 0)
    response = {"jsonrpc": "2.0", "id": message["id"], "result": {"marker": "snowman \u2603"}}
    if args.scenario == "rpc-error":
        response.pop("result")
        response["error"] = {"code": -32602, "message": "synthetic invalid params"}
    emit(response)
