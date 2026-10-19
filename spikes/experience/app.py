"""Disposable P05 fixture application. No provider, shell, or credential calls."""

import copy
import json
import os
from pathlib import Path


MODELS = {
    "fixture/sprint": {"runtime": "fixture-a", "efforts": ["low", "medium"]},
    "fixture/deliberate": {"runtime": "fixture-b", "efforts": ["medium", "high", "max"]},
}
ACCOUNTS = ("sample-personal", "sample-work")
DEFAULTS = {"model": "auto", "effort": "auto", "account": "sample-personal"}
SCENARIOS = ("normal", "login-failure", "provider-failure", "checkpoint")


class StoreError(Exception):
    pass


class Store:
    """Atomic snapshots for one fixture process per file, not production locking.

    Concurrent writers are deliberately unsupported. The production state service
    replaces this disposable JSON store with transactional ownership/revision guards.
    """

    def __init__(self, path):
        self.path = Path(path)

    def load(self):
        if not self.path.exists():
            return {"version": 1, "next_id": 1, "active": None,
                    "tasks": [], "drafts": {}, "expired": []}
        try:
            value = json.loads(self.path.read_text(encoding="utf-8"))
            if not isinstance(value, dict) or value.get("version") != 1:
                raise ValueError("unsupported fixture state version")
            if not {"version", "next_id", "active", "tasks", "drafts", "expired"} <= value.keys():
                raise ValueError("missing required state fields")
            if not isinstance(value.get("tasks"), list) or not isinstance(value.get("drafts"), dict):
                raise ValueError("invalid task or draft collection")
            if not isinstance(value.get("next_id"), int) or value["next_id"] < 1:
                raise ValueError("invalid next task identifier")
            ids = []
            for task in value["tasks"]:
                if not isinstance(task["id"], str) or not task["id"].startswith("T") or not task["id"][1:].isdigit():
                    raise ValueError("invalid task identifier")
                ids.append(task["id"])
                if not isinstance(task["attempts"], list) or not task["attempts"]:
                    raise ValueError("invalid attempts")
                for attempt in task["attempts"]:
                    if (not isinstance(attempt["prompt"], str)
                            or not isinstance(attempt["number"], int)
                            or not isinstance(attempt["revision"], int)):
                        raise ValueError("invalid prompt")
                    self.validate_requested(attempt["requested"])
                    settings = attempt["settings"]
                    if settings["model"] not in MODELS or settings["account"] not in ACCOUNTS:
                        raise ValueError("invalid fixture route")
                    if settings["effort"] not in MODELS[settings["model"]]["efforts"]:
                        raise ValueError("invalid fixture effort")
                    if attempt["status"] not in ("running", "complete", "failed", "interrupted", "canceled"):
                        raise ValueError("invalid attempt status")
            if len(ids) != len(set(ids)) or value.get("active") not in [None] + ids:
                raise ValueError("invalid active task")
            if ids and value["next_id"] <= max(int(key[1:]) for key in ids):
                raise ValueError("next task identifier would collide")
            if not isinstance(value["expired"], list) or any(a not in ACCOUNTS for a in value["expired"]):
                raise ValueError("invalid account state")
            for key, draft in value["drafts"].items():
                if key not in ["new"] + ids or not isinstance(draft["text"], str):
                    raise ValueError("invalid draft")
                self.validate_requested(draft["requested"])
            return value
        except (OSError, ValueError, KeyError, TypeError) as exc:
            raise StoreError(f"Cannot read fixture state: {exc}. Original file preserved.") from exc

    @staticmethod
    def validate_requested(value):
        if (value["model"] not in ["auto"] + list(MODELS)
                or value["effort"] not in ("auto", "low", "medium", "high", "max")
                or value["account"] not in ("auto",) + ACCOUNTS):
            raise ValueError("invalid requested settings")

    def save(self, value):
        pending = self.path.with_name(self.path.name + ".pending")
        try:
            self.path.parent.mkdir(parents=True, exist_ok=True)
            with pending.open("w", encoding="utf-8", newline="\n") as stream:
                json.dump(value, stream, ensure_ascii=False, indent=2)
                stream.write("\n")
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(pending, self.path)
        except OSError as exc:
            raise StoreError(f"Cannot save fixture state: {exc}. Input stays in memory; try /send after recovery.") from exc


class App:
    def __init__(self, store):
        self.store = store
        self.state = store.load()
        self.overrides = {}
        self.scenario = "normal"
        self.recovered = []
        for task in self.state["tasks"]:
            if task["attempts"][-1]["status"] == "running":
                task["attempts"][-1]["status"] = "interrupted"
                self.recovered.append(task["id"])
        if self.recovered:
            self.store.save(self.state)

    def active(self):
        return next((t for t in self.state["tasks"] if t["id"] == self.state["active"]), None)

    def key(self):
        return self.state["active"] or "new"

    def draft(self):
        return self.state["drafts"].get(self.key(), {"text": ""})["text"]

    def settings(self):
        task = self.active()
        draft = self.state["drafts"].get(self.key())
        base = draft["requested"] if draft else (task["attempts"][-1]["requested"] if task else DEFAULTS)
        return dict(base, **self.overrides)

    def set_draft(self, text):
        self.state["drafts"][self.key()] = {"text": text, "requested": self.settings()}
        self.store.save(self.state)

    def select(self, field, value):
        choices = {"model": ["auto"] + list(MODELS),
                   "effort": ["auto", "low", "medium", "high", "max"],
                   "account": ["auto"] + list(ACCOUNTS)}
        if value not in choices[field]:
            return [f"Unknown {field}: {value}. Open /{field}s or /effort to choose."]
        self.overrides[field] = value
        if self.draft():
            self.state["drafts"][self.key()]["requested"] = self.settings()
            self.store.save(self.state)
        scope = "next attempt in this task" if self.active() else "next task in this session"
        return [f"Selected {field}: {value} for {scope}; active work is unchanged."]

    def resolve(self, requested):
        route = dict(requested)
        if route["account"] == "auto":
            route["account"] = "sample-personal"
        if route["model"] == "auto":
            route["model"] = ("fixture/deliberate" if route["effort"] in ("high", "max")
                              else "fixture/sprint")
        if route["effort"] == "auto":
            route["effort"] = "medium"
        if route["effort"] not in MODELS[route["model"]]["efforts"]:
            raise ValueError(f"{route['model']} cannot use {route['effort']} effort. Choose /models or /effort; draft preserved.")
        route["runtime"] = MODELS[route["model"]]["runtime"]
        return route

    def submit(self, text=None, recovery=None):
        task = self.active()
        if task and task["attempts"][-1]["status"] == "running":
            if text is not None:
                self.set_draft(text)
            return ["Fixture work is at a checkpoint. Input is saved as an unsent draft.",
                    "Use /finish or /cancel; then /send. Live steering is not implemented in this spike."]
        if recovery:
            if not task:
                return ["No task selected. Use /tasks."]
            previous = task["attempts"][-1]
            allowed = {"retry": ("failed",), "resume": ("interrupted",)}
            if previous["status"] not in allowed[recovery]:
                return [f"Cannot {recovery} a {previous['status']} attempt. Follow up or choose /new."]
            prompt = previous["prompt"]
            requested = dict(previous["requested"], **self.overrides)
        else:
            if text is not None:
                self.set_draft(text)
            draft = self.state["drafts"].get(self.key())
            if not draft or not draft["text"].strip():
                return ["Draft is empty. Type a task or use /draft TEXT."]
            prompt, requested = draft["text"], draft["requested"]
        try:
            route = self.resolve(requested)
        except ValueError as exc:
            return [str(exc)]
        scenario, self.scenario = self.scenario, "normal"
        if scenario == "login-failure":
            if route["account"] not in self.state["expired"]:
                self.state["expired"].append(route["account"])
            self.store.save(self.state)
        if route["account"] in self.state["expired"]:
            return [f"SIMULATED login expired for {route['account']}; nothing dispatched.",
                    "Draft preserved. Use /reconnect, then /send (or repeat the recovery action)."]
        before = copy.deepcopy(self.state)
        old_key = self.key()
        if task is None:
            task = {"id": f"T{self.state['next_id']}", "attempts": []}
            self.state["next_id"] += 1
            self.state["tasks"].append(task)
            self.state["active"] = task["id"]
        previous = task["attempts"][-1] if task["attempts"] else None
        revision = (previous["revision"] if recovery else previous["revision"] + 1) if previous else 1
        attempt = {"number": len(task["attempts"]) + 1, "revision": revision,
                   "prompt": prompt, "requested": dict(requested), "settings": route,
                   "status": "running", "result": None, "fixture": True}
        task["attempts"].append(attempt)
        if not recovery:
            self.state["drafts"].pop(old_key, None)
        try:
            self.store.save(self.state)
        except StoreError:
            self.state = before
            raise
        self.overrides = {}
        messages = [f"ACCEPTED {task['id']} attempt {attempt['number']} revision {revision} [FIXTURE]",
                    f"Effective: {route['model']} / {route['effort']} / {route['account']}"]
        if previous and previous["settings"]["runtime"] != route["runtime"]:
            messages += ["Runtime changed: bounded fixture handoff carries prior inputs/results.",
                         "No native memory is reused. Tools differ: fixture-a check / fixture-b review."]
        if scenario == "provider-failure":
            attempt["status"] = "failed"
            attempt["result"] = "SIMULATED provider failure before any external effect"
            self.store.save(self.state)
            return messages + ["SIMULATED provider failure. Task/input preserved; use /retry."]
        if scenario == "checkpoint":
            return messages + ["SIMULATED running checkpoint; no background process exists.",
                               "Use /finish, /cancel, or exit and /resume after restart."]
        return messages + self.finish()

    def finish(self):
        task = self.active()
        if not task or task["attempts"][-1]["status"] != "running":
            return ["No running fixture task. Use /tasks to inspect work."]
        before = copy.deepcopy(self.state)
        attempt = task["attempts"][-1]
        attempt["status"] = "complete"
        attempt["result"] = (f"Fixture response to: {attempt['prompt']}\n"
                             "Illustrative result only. No model, tools, checks, or files were changed.")
        try:
            self.store.save(self.state)
        except StoreError:
            self.state = before
            raise
        return [f"SIMULATED COMPLETE {task['id']} attempt {attempt['number']}",
                attempt["result"], "Follow up here, inspect /result, or start /new."]

    def cancel(self, interrupted=False):
        task = self.active()
        if not task or task["attempts"][-1]["status"] != "running":
            return ["No running attempt. Draft and prior results are preserved."]
        before = copy.deepcopy(self.state)
        task["attempts"][-1]["status"] = "interrupted" if interrupted else "canceled"
        try:
            self.store.save(self.state)
        except StoreError:
            self.state = before
            raise
        return [f"{task['id']} {'interrupted' if interrupted else 'canceled'}; prior evidence and drafts preserved.",
                "No external effects exist in this fixture. " +
                ("Use /resume or a new follow-up." if interrupted else "Use a new follow-up or /new.")]

    def reconnect(self):
        account = self.settings()["account"]
        account = "sample-personal" if account == "auto" else account
        self.state["expired"] = [a for a in self.state["expired"] if a != account]
        self.store.save(self.state)
        return [f"SIMULATED reconnect complete for {account}; no provider login or credential changed.",
                "Use /send for the preserved draft, or repeat /retry or /resume."]

    def open_task(self, task_id):
        if not any(t["id"] == task_id for t in self.state["tasks"]):
            return [f"No task {task_id}. Use /tasks."]
        if self.active() and self.active()["id"] != task_id and self.active()["attempts"][-1]["status"] == "running":
            return ["Finish or cancel the foreground fixture before switching tasks."]
        self.state["active"] = task_id
        self.overrides = {}
        self.store.save(self.state)
        return [f"Opened {task_id}; no attempt started."]

    def new_task(self):
        if self.active() and self.active()["attempts"][-1]["status"] == "running":
            return ["Finish or cancel the foreground fixture before /new. Detach is unavailable."]
        self.state["active"] = None
        self.overrides = {}
        self.store.save(self.state)
        return ["New task selected. Earlier task context and drafts remain in /tasks."]
