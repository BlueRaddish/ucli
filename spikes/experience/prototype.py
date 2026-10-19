"""Accessible line renderer for the disposable P05 application."""

import argparse
from pathlib import Path
import sys
import tempfile

from app import ACCOUNTS, App, MODELS, SCENARIOS, Store, StoreError


HELP = """NAME
  ucli-experience - try the P05 terminal fixture
USAGE
  python spikes/experience/prototype.py [--state FILE]
DESCRIPTION
  explore task continuity and recovery using synthetic data only
EXAMPLES
  start with a separate fixture history
  $ python spikes/experience/prototype.py --state scratch/p05.json
  inspect these controls without starting a session
  $ python spikes/experience/prototype.py --help
COMMANDS
  /new              start a fresh task; preserve earlier drafts
  /models           choose a model for the next task/attempt
  /accounts         choose a synthetic account
  /effort           choose a shared effort level
  /tasks            open a task without starting work
  /result           inspect every attempt and previous result
  /draft TEXT       save text without sending it; omit TEXT to inspect
  /send             submit the saved draft
  /retry            retry a failed fixture attempt
  /resume           resume a reconciled interrupted fixture
  /cancel           stop the running fixture; retain its input
  /finish           complete a fixture waiting at its checkpoint
  /reconnect        repair simulated login; never contact a provider
  /scenario         choose a one-shot simulated failure/checkpoint
  /native           inspect native-mode limits; launch nothing
  /detach           explain why foreground-only work cannot detach
  /help             show these controls
  /exit             save and leave; running fixtures become interrupted
OPTIONS
  -h, --help        show help and exit
      --state FILE  set isolated fixture state (default: OS temporary directory)
EXIT STATUS
  0  fixture session exited
  2  invalid invocation
  9  fixture state cannot be read or saved
  130  interrupted outside the input loop
SEE ALSO
  spikes/experience/README.md
REPORT BUGS
  record prototype observations in evidence/usability/phase0/
"""


def safe(text):
    # Provider/user text must not issue terminal control sequences in unified mode.
    return "".join(ch if ch in "\n\t" or ch.isprintable()
                   else f"\\x{ord(ch):02x}" for ch in str(text))


def emit(*lines):
    for line in lines:
        print(safe(line), flush=True)


def choose(title, rows, read=input):
    """Return a value, cancel, or propagate terminal EOF/interrupt without mutation."""
    visible = list(rows)
    while True:
        emit(title)
        for index, (value, label) in enumerate(visible, 1):
            emit(f"  {index}. {label}")
        emit("  0. Back     Type a number or search text; blank clears search")
        answer = read("Choose > ").strip()
        if answer == "0":
            return None
        if answer.isdigit():
            index = int(answer) - 1
            if 0 <= index < len(visible):
                return visible[index][0]
            emit("Choose a listed number, or 0 to go back.")
        else:
            visible = [row for row in rows if answer.casefold() in row[1].casefold()]
            if not visible:
                emit("No matches. Change the search or press Enter to show all.")


def status(app):
    task = app.active()
    selected = app.settings()
    if task:
        last = task["attempts"][-1]
        emit(f"Task {task['id']} | {last['status']} | {len(task['attempts'])} attempt(s)")
    else:
        emit("New task | fresh context")
    emit("Next: " + " / ".join(selected[k] for k in ("model", "effort", "account")))
    if app.draft():
        emit(f"Saved draft: {app.draft()}")
    emit("Actions: /new /models /accounts /effort /tasks /result /help /exit")
    if task and task["attempts"][-1]["status"] == "running":
        emit("Checkpoint: /finish /cancel | no live steering or Detach in this fixture")
    elif task and task["attempts"][-1]["status"] in ("failed", "interrupted"):
        emit("Recovery: /retry for failed | /resume for interrupted | /result")
    elif task and task["attempts"][-1]["status"] == "canceled":
        emit("Canceled attempt retained. Follow up with new work, choose /new, or inspect /result.")
    if app.draft():
        emit("Draft: /send | /draft TEXT to replace | /reconnect for simulated login")


def result(app):
    task = app.active()
    if not task:
        emit("No task selected. Use /tasks or describe a new task.")
        return
    for attempt in task["attempts"]:
        emit(f"{task['id']} attempt {attempt['number']} revision {attempt['revision']} [{attempt['status']}]",
             "Input: " + attempt["prompt"], attempt["result"] or "No terminal result recorded.")


def command(app, line):
    verb, _, argument = line.partition(" ")
    if verb in ("/models", "/accounts", "/effort"):
        field = {"/models": "model", "/accounts": "account", "/effort": "effort"}[verb]
        if field == "model":
            rows = [("auto", "Auto - deterministic fixture choice")]
            rows += [(key, f"{key} - {value['runtime']} - {', '.join(value['efforts'])}")
                     for key, value in MODELS.items()]
        elif field == "account":
            rows = [("auto", "Auto - sample-personal eligible pool only")]
            rows += [(key, key + (" - SIMULATED login expired" if key in app.state["expired"] else " - fixture ready"))
                     for key in ACCOUNTS]
        else:
            rows = [(key, key) for key in ("auto", "low", "medium", "high", "max")]
        selected = choose(f"Choose {field} (fixture only)", rows)
        if selected is not None:
            emit(*app.select(field, selected))
    elif verb == "/tasks":
        rows = [(t["id"], f"{t['id']} - {t['attempts'][-1]['status']} - {t['attempts'][0]['prompt'][:60]}")
                for t in app.state["tasks"]]
        if not rows:
            emit("No tasks yet. Type a task at the prompt.")
        else:
            selected = choose("Open a task; no work starts", rows)
            if selected:
                emit(*app.open_task(selected))
    elif verb == "/scenario":
        selected = choose("Fixture scenario for the next dispatch", [(v, v) for v in SCENARIOS])
        if selected:
            app.scenario = selected
            emit(f"SIMULATION: next dispatch uses {selected}; no real provider is involved.")
    elif verb == "/draft":
        if argument:
            app.set_draft(argument)
            emit("Draft saved; not sent.")
        else:
            emit("Draft: " + (app.draft() or "empty"))
    elif verb == "/new":
        emit(*app.new_task())
    elif verb == "/send":
        emit(*app.submit())
    elif verb in ("/retry", "/resume"):
        emit(*app.submit(recovery=verb[1:]))
    elif verb == "/finish":
        emit(*app.finish())
    elif verb == "/cancel":
        emit(*app.cancel())
    elif verb == "/reconnect":
        emit(*app.reconnect())
    elif verb == "/result":
        result(app)
    elif verb == "/detach":
        emit("Detach unavailable: this fixture is foreground-only, with no supervisor.",
             "Exit marks running work interrupted; /resume after restart creates a fresh fixture attempt.")
    elif verb == "/native":
        emit("NATIVE MODE PREVIEW ONLY: a real native route would use its own controls/output.",
             "Unified steering, account isolation and recovery would require declared capabilities.",
             "This fixture launches no native program and changes no credentials.")
    elif verb == "/help":
        emit(HELP)
    else:
        emit(f"Unknown action {verb}. Use /help; text beginning / can be entered with /draft TEXT.")


def main(argv=None):
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--state", default=str(Path(tempfile.gettempdir()) / "ucli-p05-experience.json"))
    argv = sys.argv[1:] if argv is None else argv
    if "--help" in argv or "-h" in argv:
        emit(HELP)
        return 0
    args = parser.parse_args(argv)
    try:
        app = App(Store(args.state))
    except StoreError as exc:
        print(safe(exc), file=sys.stderr)
        return 9
    emit("UCLI P05 EXPERIENCE FIXTURE - not the UCLI product",
         "SIMULATION ONLY: no model calls, filesystem task effects, or provider logins.",
         f"Fixture state: {Path(args.state).absolute()}",
         "One process per state file. Single-line input; no full TUI/paste support.")
    if app.recovered:
        emit("Recovered interrupted fixture: " + ", ".join(app.recovered), "Use /resume after inspecting /result.")
    try:
        while True:
            status(app)
            task = app.active()
            label = "Checkpoint input" if task and task["attempts"][-1]["status"] == "running" else (
                "Follow up on this task" if task else "Describe a new task")
            try:
                line = input(label + " > ").strip()
                if line == "/exit":
                    emit(*app.cancel(interrupted=True))
                    emit("Fixture session saved. No work continues in the background.")
                    return 0
                if not line:
                    continue
                if line.startswith("/"):
                    command(app, line)
                else:
                    emit(*app.submit(line))
            except KeyboardInterrupt:
                emit("\nInterrupt received.", *app.cancel())
            except StoreError as exc:
                emit(str(exc))
            except EOFError:
                emit(*app.cancel(interrupted=True))
                emit("Input closed; accepted drafts/results preserved. No background execution.")
                return 0
    except (StoreError, OSError) as exc:
        print(safe(exc), file=sys.stderr)
        return 9


if __name__ == "__main__":
    raise SystemExit(main())
