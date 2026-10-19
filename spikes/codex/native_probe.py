"""No-prompt native probe. Never reads/copies credentials or native history.

Only selected fields from native responses enter receipts. Each run uses a new
synthetic CODEX_HOME/profile, never the operator's ordinary Codex configuration.
"""
import argparse
from collections import Counter
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

from driver import Driver, ProtocolError, sanitized_account, sanitized_models


def isolated_environment(root):
    # Deliberately not an inherited copy minus a handful of known secret names.
    permitted = {"systemroot", "windir", "path", "pathext", "comspec", "programfiles", "programfiles(x86)",
                 "programw6432", "number_of_processors", "processor_architecture"}
    environment = {key: value for key, value in os.environ.items() if key.lower() in permitted}
    for name, part in {"HOME": "profile", "USERPROFILE": "profile", "APPDATA": "roaming",
                       "LOCALAPPDATA": "local", "TEMP": "temp", "TMP": "temp", "CODEX_HOME": "codex-home"}.items():
        target = root / part
        target.mkdir(exist_ok=True)
        environment[name] = str(target)
    return environment


def run_case(executable, root, trusted):
    environment = isolated_environment(root)
    repository = root / "workspace"
    repository.mkdir(parents=True)
    subprocess.run(["git", "-c", "init.templateDir=", "init", "--quiet", str(repository)], env=environment,
                   check=True, capture_output=True, timeout=10,
                   creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
    (repository / ".codex").mkdir()
    marker = root / "hook-sentinel"
    script = root / "sentinel.py"
    script.write_text("from pathlib import Path\nPath(" + repr(str(marker)) + ").write_text('synthetic')\n", encoding="utf-8")
    # Config is synthetic. Command is never authorized/trusted by this probe.
    hook_command = subprocess.list2cmdline([sys.executable, str(script)])
    project_config = '\n'.join([
        'model_provider = "ucli_sentinel"',
        '[model_providers.ucli_sentinel]', 'name = "UCLI synthetic non-network endpoint"',
        'base_url = "http://127.0.0.1:9/ucli-never-infer"', 'wire_api = "responses"',
        'requires_openai_auth = false',
        '[[hooks.SessionStart]]', 'matcher = "startup|resume"',
        '[[hooks.SessionStart.hooks]]', 'type = "command"', 'command = ' + json.dumps(hook_command),
        'timeout = 2', '',
    ])
    (repository / ".codex/config.toml").write_text(project_config, encoding="utf-8")
    user_config = '\n'.join([
        'cli_auth_credentials_store = "ephemeral"', 'approval_policy = "never"',
        'sandbox_mode = "read-only"', 'web_search = "disabled"',
        '[history]', 'persistence = "none"', '[analytics]', 'enabled = false',
        '[features]', 'apps = false', 'hooks = true',
        '[projects.' + json.dumps(str(repository)) + ']',
        'trust_level = ' + json.dumps("trusted" if trusted else "untrusted"), '',
    ])
    (Path(environment["CODEX_HOME"]) / "config.toml").write_text(user_config, encoding="utf-8")
    driver = Driver([str(executable), "app-server", "--stdio"], repository, environment)
    result = {"case": "project-trusted-hook-untrusted" if trusted else "project-untrusted",
              "home": "fresh-synthetic", "auth_storage": "ephemeral", "environment": "allowlist",
              "turn_start_count": 0, "credential_helper_sentinel": "not-tested-no-confirmed-hook-interface"}
    try:
        initialized = driver.initialize()
        result["initialized"] = isinstance(initialized, dict)
        result["account"] = sanitized_account(driver.request("account/read", {"refreshToken": False}))
        result["catalog"] = sanitized_models(driver.request("model/list", {"limit": 100, "includeHidden": False}))
        config = driver.request("config/read", {"cwd": str(repository), "includeLayers": True})
        layers = config.get("layers") or []
        result["config_layers"] = [{"source": layer.get("name", {}).get("type"),
                                    "disabled": bool(layer.get("disabledReason")),
                                    "key_count": len(layer.get("config", {})),
                                    "has_sentinel_provider": "ucli_sentinel" in layer.get("config", {}).get("model_providers", {})}
                                   for layer in layers]
        result["effective_sentinel_provider"] = config.get("config", {}).get("model_provider") == "ucli_sentinel"
        result["effective_hook_config_present"] = bool(config.get("config", {}).get("hooks"))
        try:
            hooks = driver.request("hooks/list", {"cwds": [str(repository)]})
            result["hooks"] = [{key: hook.get(key) for key in ("eventName", "source", "trustStatus", "isManaged", "enabled")}
                               for entry in hooks.get("data", []) for hook in entry.get("hooks", [])]
        except ProtocolError:
            result["hooks"] = "metadata-interface-unavailable"
        source_types = {layer["source"] for layer in result["config_layers"] if layer["key_count"]}
        if source_types - {"packagedDefaults", "user", "project", "sessionFlags"}:
            result["thread_start"] = "skipped-unreviewed-managed-layer"
        elif result["account"]["present"]:
            result["thread_start"] = "skipped-unexpected-account-in-isolated-home"
        else:
            # Session creation has no user input. No turn/start is permitted.
            driver.start_thread(repository)
            result["thread_start"] = "created-without-prompt"
            result["reported_policy"] = driver.thread_policy
            driver.observe(1)
        result["hook_sentinel_executed"] = marker.exists()
        result["turn_start_count"] = driver.sent_methods.count("turn/start")
        result["events"] = dict(Counter(frame.get("method") for frame in driver.events))
        warnings = json.dumps([frame.get("params") for frame in driver.events if frame.get("method") == "configWarning"]).lower()
        result["warning_keywords"] = [term for term in ("unsupported", "project", "model_provider", "hooks", "trust") if term in warnings]
        result["stderr_bytes_discarded"] = driver.stderr_bytes
        result["protocol_result"] = "completed"
    except (ProtocolError, KeyError, TypeError) as error:
        # ProtocolError text is generated by the driver and contains no response body.
        result["protocol_result"] = "failed"
        result["error_class"] = type(error).__name__
        if isinstance(error, ProtocolError):
            result["error"] = str(error)
        result["hook_sentinel_executed"] = marker.exists()
        result["turn_start_count"] = driver.sent_methods.count("turn/start")
        result["stderr_bytes_discarded"] = driver.stderr_bytes
    finally:
        driver.close()
    result["hook_sentinel_after_cleanup"] = marker.exists()
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--codex-exe", type=Path, required=True, help="absolute native binary, not shell wrapper")
    parser.add_argument("--receipt", type=Path, required=True)
    args = parser.parse_args()
    executable = args.codex_exe.resolve(strict=True)
    version = subprocess.run([str(executable), "--version"], capture_output=True, timeout=10,
                             creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
    if version.returncode != 0:
        raise SystemExit("codex version probe failed")
    runtime_version = version.stdout.decode().strip()
    if runtime_version != "codex-cli 0.162.1":
        raise SystemExit("native probe blocked: regenerate/review schema and trust checks for this runtime version")
    scratch = Path(__file__).resolve().parent / ".scratch"
    scratch.mkdir(exist_ok=True)
    cases = []
    started = time.monotonic()
    with tempfile.TemporaryDirectory(dir=scratch) as directory:
        for trusted in (False, True):
            root = Path(directory) / ("trusted" if trusted else "untrusted")
            root.mkdir()
            cases.append(run_case(executable, root, trusted))
    untrusted, trusted = cases
    checks = {
        "protocol_completed": all(case["protocol_result"] == "completed" for case in cases),
        "isolated_account_absent": all(not case.get("account", {}).get("present", True) for case in cases),
        "project_layer_disabled_when_untrusted": any(layer["source"] == "project" and layer["disabled"] for layer in untrusted.get("config_layers", [])),
        "untrusted_project_hooks_absent": untrusted.get("hooks") == [] and not untrusted.get("effective_hook_config_present", True),
        "trusted_project_hook_discovered_but_untrusted": isinstance(trusted.get("hooks"), list) and any(hook.get("source") == "project" and hook.get("trustStatus") == "untrusted" for hook in trusted["hooks"]),
        "project_endpoint_not_effective": all(not case.get("effective_sentinel_provider", True) for case in cases),
        "hook_sentinel_did_not_execute": all(not case["hook_sentinel_after_cleanup"] for case in cases),
        "no_prompt_dispatched": all(case["turn_start_count"] == 0 for case in cases),
        "no_prompt_thread_created": all(case.get("thread_start") == "created-without-prompt" for case in cases),
        "read_only_reported_not_enforcement_proved": all(case.get("reported_policy", {}).get("sandbox_type") == "readOnly" for case in cases),
    }
    exit_code = 0 if all(checks.values()) else 1
    receipt = {"date_utc": datetime.now(timezone.utc).isoformat(), "scope": "real installed binary; synthetic home; no prompt/inference",
               "runtime": runtime_version, "binary_sha256": hashlib.sha256(executable.read_bytes()).hexdigest(),
               "contract_revision": "spike-v0-2026-10-18", "platform": platform.system(), "architecture": platform.machine(),
               "python": platform.python_version(), "elapsed_seconds": round(time.monotonic() - started, 3),
               "cases": cases, "checks": checks, "exit_code": exit_code,
               "live_inference": {"status": "blocked", "turns": 0, "interrupt_probes": 0,
                  "reasons": ["Existing account binding is unproved in a fresh isolated CODEX_HOME without moving credentials.",
                              "Managed/native tools and credential-helper trust boundaries are not completely verified."]}}
    root = Path(__file__).resolve().parents[2]
    receipt["source_sha256"] = {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in
                                ("spikes/codex/driver.py", "spikes/codex/native_probe.py")}
    args.receipt.parent.mkdir(parents=True, exist_ok=True)
    args.receipt.write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(receipt, indent=2))
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
