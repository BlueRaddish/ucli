# P05 terminal experience fixture

This is a runnable, disposable UX spike. It uses Python's standard library and
synthetic models/accounts. It makes no model calls, starts no native agent, reads
no provider configuration, and changes no credentials. “Complete” in its output
always means **simulated completion**, never a real coding result.

From this worktree:

```powershell
python spikes/experience/prototype.py --state "$env:TEMP/ucli-p05-owner.json"
```

On macOS/Linux the equivalent is:

```sh
python3 spikes/experience/prototype.py --state /tmp/ucli-p05-owner.json
```

Use a separate state file for each concurrent reviewer. Without `--state`, the
fixture uses `ucli-p05-experience.json` in the operating system's temporary
directory. Reusing a file restores its history and saved drafts. It is local,
unencrypted **synthetic test data**: do not enter credentials or private work.
Remove your own fixture JSON when you no longer need its history.

Type an ordinary one-line goal at the prompt. Subsequent goals follow up in the
same visible task. The action line exposes model, account, effort, new-task and
history choices. Selectors accept a number or search text; `0` returns without
changing the current draft. `/help` explains the complete fixture controls.

The choices are deliberately fictitious:

| Choice | Fixture behavior |
| --- | --- |
| Auto model/effort | chooses a compatible model deterministically |
| `fixture/sprint` | supports low/medium effort; fixture-a tools |
| `fixture/deliberate` | supports medium/high/max effort; fixture-b tools |
| Auto account | stays in the sample-personal eligible pool |
| sample-personal / sample-work | explicit synthetic account choices |
| Native | explains reduced integration; launches nothing |

Changing runtime shows that the handoff carries prior inputs/results and that
the fictitious tools differ. Prior attempts retain their original route and
result. `/new` creates fresh context and restores defaults; selecting a model or
account affects only this session's next task/attempt. A rejected setting keeps
the draft and does not silently override an explicit model.

## Recovery scenarios

`/scenario` selects one failure for the next dispatch. Normal work has no
artificial delay. A checkpoint stays open only because the reviewer selected
that explicit fixture scenario.

| Scenario | Observable recovery |
| --- | --- |
| Login failure | no task dispatched; draft/account survive exit and restart; `/reconnect`, then `/send` |
| Provider failure | accepted task retains failed evidence; `/retry` creates another attempt |
| Checkpoint | `/finish` completes, `/cancel` stops, EOF/exit interrupts; restart and `/resume` retain task ID |
| Abrupt process termination | startup identifies accepted running fixture as interrupted before offering resume |

`/draft TEXT` saves without submitting, and `/send` submits the saved draft.
Drafts remain attached to their original task when navigating away. Restart
shows them but never sends them automatically. `/result` exposes every prior
attempt. Cancellation preserves the old attempt; a deliberate follow-up can
create new work without removing its evidence.

The fixture is foreground-only. `/detach` explains this limit. No background
execution continues after leaving. Input during the simulated running checkpoint
is saved **unsent**, with an explicit instruction to finish/cancel and then send;
live steering is not implemented or claimed.

## Reusable checks

```powershell
python spikes/experience/check.py --receipt evidence/usability/phase0
```

This consumes the real CLI in child processes and checks persisted state and
visible output. It terminates one owned child, restarts it, and causes a real
filesystem write failure. Windows children use `CREATE_NO_WINDOW` with captured
output. Evidence files survive the disposable test directories.

`app.py` holds fixture application/state behavior and accepts a store. `prototype.py`
owns terminal text, menus and input. The separation is for later Go adaptation;
the Python files are not a production runtime or an adapter contract.

Known ceilings: one process per JSON file, single-line input, no bracketed paste,
no per-keystroke recovery, no supervisor, no real streaming/tools, and no native
account/session isolation. Atomic snapshots protect committed fixture records;
they do not provide production concurrency or schema migration guarantees. The
production state service replaces this store. Ctrl+C is handled in the renderer,
but the physical Windows key path still needs a real terminal review.

The retained acceptance record establishes fixture behavior only. Owner usability,
screen-reader behavior, actual terminals on macOS/Linux, and real providers are
unobserved. Use the [natural-goal walkthrough](../../docs/verification.md)
before teaching the reviewer command names.
