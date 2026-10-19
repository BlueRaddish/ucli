"""Synthetic JSONL producer using the installed app-server method/event shapes.

No provider calls; fixture scenarios are intentionally adversarial.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

scenario = sys.argv[1]
thread_id = "thread_synthetic"
turn_count = 0
active = None
manifest = json.loads((Path(__file__).parent / "protocol-0.162.1.json").read_text(encoding="utf-8"))


def emit(frame):
    data = json.dumps(frame, ensure_ascii=False).encode() + b"\n"
    if scenario == "split-utf8":
        for byte in data:
            sys.stdout.buffer.write(bytes([byte]))
            sys.stdout.buffer.flush()
    else:
        sys.stdout.buffer.write(data)
        sys.stdout.buffer.flush()


def done(status):
    emit({"method": "turn/completed", "params": {"threadId": thread_id,
          "turn": {"id": active, "status": status, "items": [], "error": None}}})


for line in sys.stdin.buffer:
    request = json.loads(line)
    method = request.get("method")
    if method is None:
        if request.get("id") == "approval_fixture":
            assert request["result"]["decision"] == "decline"
            done("completed")
        if request.get("id") == "unknown_fixture":
            assert request["error"]["code"] == -32601
            done("completed")
        continue
    if method == "initialized":
        if scenario == "no-read":
            time.sleep(30)
        continue
    if method in manifest["methods"]:
        shape = manifest["schemas"][manifest["methods"][method]]
        assert set(shape["required"]) <= set(request["params"])
        assert set(request["params"]) <= set(shape["properties"])
    if method == "initialize":
        if scenario == "malformed":
            sys.stdout.buffer.write(b"{malformed}\n")
            sys.stdout.buffer.flush()
            continue
        if scenario == "oversize":
            sys.stdout.buffer.write(b"x" * (1024 * 1024 + 1))
            sys.stdout.buffer.flush()
            continue
        if scenario == "truncated":
            sys.stdout.buffer.write(b'{"incomplete":')
            sys.stdout.buffer.flush()
            break
        if scenario == "invalid-utf8":
            sys.stdout.buffer.write(b'{"bad":"\xff"}\n')
            sys.stdout.buffer.flush()
            continue
        if scenario == "wrong-id":
            emit({"id": 9000, "result": {}})
            continue
        if scenario == "stderr-flood":
            sys.stderr.buffer.write(b"synthetic diagnostic\n" * 100000)
            sys.stderr.buffer.flush()
        if scenario == "event-flood":
            for number in range(2000):
                emit({"method": "fixture/flood", "params": {"number": number}})
        result = {"userAgent": "codex-fixture/0.162.1"}
    elif method == "account/read":
        result = {"account": {"type": "chatgpt", "email": "fixture@example.invalid", "planType": "unknown"},
                  "workspaceRouting": {"chatgptAccountId": "synthetic-identity"}, "requiresOpenaiAuth": True}
    elif method == "model/list":
        result = {"data": [{"id": "fixture/model", "model": "fixture/model", "defaultReasoningEffort": "low"}],
                  "nextCursor": None}
    elif method in ("thread/start", "thread/resume"):
        assert "baseInstructions" not in request["params"]
        assert "developerInstructions" not in request["params"]
        if method == "thread/resume":
            assert request["params"]["excludeTurns"] is True
        result = {"thread": {"id": thread_id, "turns": []}}
    elif method == "turn/start":
        turn_count += 1
        active = "turn_synthetic_" + str(turn_count)
        result = {"turn": {"id": active, "status": "inProgress", "items": [], "error": None}}
    elif method == "turn/interrupt":
        result = {}
    elif method == "fixture/child":
        marker = Path(request["params"]["heartbeat"])
        child_code = "import time; from pathlib import Path; p=Path(" + repr(str(marker)) + ");\nwhile True: p.write_text(str(time.monotonic())); time.sleep(.02)"
        subprocess.Popen([sys.executable, "-c", child_code], stdin=subprocess.DEVNULL,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                         creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        result = {"started": True}
    else:
        emit({"id": request["id"], "error": {"code": -32601, "message": "fixture method unsupported"}})
        continue
    emit({"id": request["id"], "result": result})
    if method == "turn/interrupt" and scenario != "interrupt-unconfirmed":
        done("interrupted")
    if method == "turn/start":
        emit({"method": "turn/started", "params": {"threadId": thread_id, "turn": result["turn"]}})
        if scenario == "crash":
            break
        if scenario in ("hang", "interrupt-unconfirmed"):
            continue
        if scenario == "approval":
            emit({"id": "approval_fixture", "method": "item/commandExecution/requestApproval",
                  "params": {"threadId": thread_id, "turnId": active}})
            continue
        if scenario == "unknown-request":
            emit({"id": "unknown_fixture", "method": "fixture/unknown", "params": {}})
            continue
        emit({"method": "item/agentMessage/delta", "params": {"threadId": thread_id,
              "turnId": active, "itemId": "message_synthetic", "delta": "fixture marker — α"}})
        done("failed" if scenario == "failed" else "completed")
