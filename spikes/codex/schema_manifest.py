"""Extract a compact shape/fingerprint ledger from the installed native schemas.

This is schema drift evidence, not a complete JSON Schema validator.
"""
import argparse
import hashlib
import json
from pathlib import Path

METHODS = {
    "initialize": "v1/InitializeParams.json",
    "account/read": "v2/GetAccountParams.json",
    "model/list": "v2/ModelListParams.json",
    "config/read": "v2/ConfigReadParams.json",
    "hooks/list": "v2/HooksListParams.json",
    "thread/start": "v2/ThreadStartParams.json",
    "thread/resume": "v2/ThreadResumeParams.json",
    "turn/start": "v2/TurnStartParams.json",
    "turn/interrupt": "v2/TurnInterruptParams.json",
}
RESPONSES = ["v1/InitializeResponse.json", "v2/GetAccountResponse.json", "v2/ModelListResponse.json",
             "v2/ConfigReadResponse.json", "v2/HooksListResponse.json", "v2/ThreadStartResponse.json",
             "v2/ThreadResumeResponse.json", "v2/TurnStartResponse.json", "v2/TurnInterruptResponse.json",
             "v2/TurnCompletedNotification.json", "v2/AgentMessageDeltaNotification.json"]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--schema-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--runtime-version", required=True)
    args = parser.parse_args()
    entries = {}
    for name in list(METHODS.values()) + RESPONSES:
        raw = (args.schema_dir / name).read_bytes()
        schema = json.loads(raw)
        entries[name] = {"sha256": hashlib.sha256(raw).hexdigest(), "title": schema.get("title"),
                         "required": schema.get("required", []), "properties": sorted(schema.get("properties", {}))}
    args.output.write_text(json.dumps({"runtime": args.runtime_version, "source": "installed binary generate-json-schema",
                                      "methods": METHODS, "schemas": entries}, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
