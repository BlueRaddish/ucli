# UCLI experience contract

Status: product contract, originally 2026-10-17. The experimental subset is tracked
in [the development slice](development-slice.md); proposed commands below are not
all implemented. No human usability result is claimed. UCLI is a separate product; development must leave
the existing cly installation, configuration, sessions, and commands usable.

## Product promise and decisions

UCLI lets a person describe work once, choose a model or accept a sensible
automatic choice, and keep working through account limits and interruptions.
The primary quality measure is successful work with little remembering,
configuration, waiting, or recovery effort. Provider breadth is valuable only
when the common experience remains understandable.

The interface has one vocabulary across terminal menus, commands, help, events,
and documentation. Use **model**, **effort**, **account**, **task**, **workflow**,
and **permission** consistently. A runtime is the software that executes an
agent; most users encounter it only when choosing between materially different
capabilities or inspecting details. Do not describe an account as a profile in
one surface and a connection in another.

The initial interaction is a compact prompt with visible choices. A full-screen
dashboard, a command-only interface, and a prompt with a small task list were
considered using the same content. Choose the third: it keeps the user's work
and next action prominent, retains terminal scrollback, and allows recognition
without first learning commands. A full-screen task monitor can come later if
real concurrent use justifies its navigation and accessibility costs.

Defaults should get safer and easier without silently changing an explicit
choice. The first useful task must not require an account dashboard, a workflow
file, a router configuration, or a GitHub login. GitHub identity is optional
cross-device setup and synchronization; provider authorization remains distinct.

## The common language

These are proposed commands, not commands available today:

| Command | User purpose |
| --- | --- |
| `ucli` | open the interactive home in a terminal |
| `ucli run PROMPT` | start work using defaults or explicit settings |
| `ucli models` | find available models and select one interactively |
| `ucli accounts` | inspect connected accounts, capacity, and connection actions |
| `ucli tasks` | find, inspect, continue, or stop work |
| `ucli workflow` | find, inspect, and run reusable task plans |
| `ucli explain PROMPT` | preview a route and its reasons without starting work |
| `ucli native RUNTIME -- ARGS...` | launch the native runtime explicitly |
| `ucli doctor` | diagnose a specific setup or execution problem |
| `ucli login [PROVIDER]` | connect a native account; GitHub currently uses the local CLI identity |

The recovery commands share the visible task actions:

| Command | User purpose |
| --- | --- |
| `ucli tasks show ID` | inspect a task, its outcome, and available recovery |
| `ucli tasks attach ID` | attach to existing work without starting execution |
| `ucli tasks resume ID` | continue from a supported checkpoint |
| `ucli tasks retry ID` | create an eligible attempt for a failed step |
| `ucli tasks cancel ID` | request cancellation and preserve partial outputs |
| `ucli workflow inspect ID` | inspect a workflow and its constituent task IDs |
| `ucli workflow attach ID` | attach to a workflow's existing run |

Task commands take task IDs; workflow commands resolve workflow IDs and expose
their task IDs for task-specific recovery. No command infers its target from the
most recently active task in another terminal.

Shared run settings are `--model`, `--effort`, `--account`, `--cwd`,
`--permission`, and `--output`. More specialized settings remain in command
detail or named presets. Avoid adding synonyms just because each provider has
one. Preserve conventional global flags, including `-h/--help`, `-V/--version`,
`-v/--verbose`, `--json`, `--plain`, `--no-color`, and `--no-input`.

Model and effort default to `auto`. Account selection defaults to the workspace's
bound account or the user's previously authorized eligible account set. A new
installation never treats every discovered login as interchangeable. Explicit
`--account auto` optimizes within that set and cannot broaden it; personal/work
boundaries remain binding. An explicit model constrains routing; `--effort auto`
may select that model's
effort but cannot override the model. An explicit account is similarly binding.
An unsupported explicit effort is an actionable error, not a silent downgrade.
The model picker shows provider-qualified stable identifiers, friendly labels,
supported effort levels, and relevant capabilities. Short aliases are permitted
only when unambiguous; collisions offer choices and never guess.

Proposed effort values are `auto`, `low`, `medium`, `high`, and `max`. Adapters
declare supported intent mappings and expose the exact native setting. `max`
means the model's highest supported effort within the existing budget; effort
labels do not assert equal computation across providers.

Effort controls computation. Permissions control authority. Higher effort never
grants file, network, shell, or publication permission. A remembered preset can
combine settings, but its permission scope must remain visible at execution.
Permission names are `read-only`, `workspace-write`, `sandbox`, and `unrestricted`.
The owner's `sandbox` option means full local computer access and is labelled that
way in the selector. It is explicit, remains recorded on the task, and maps to a
native broad-access mode; the default remains `read-only`.
Untrusted workspaces begin read-only; recorded workspace trust or explicit user
authorization can enable workspace writes. Unrestricted authority is explicitly
scoped and never selected by the router. Network/tool approvals remain separate
policy dimensions. An adapter must enforce the selected scope or refuse it;
an instruction telling a model to behave is not permission enforcement.

## First useful task

1. Launch `ucli`. Local discovery reads supported installation and authorization
   status without switching accounts or rewriting native configuration.
2. If one usable connection exists, show it as the ready choice. If none exists,
   offer a searchable connection list and each provider's supported login path.
   Preserve the prompt if login fails or the browser is closed.
3. Type a task or choose a visible action. Retain the current directory or `--cwd`
   as the execution directory. Discover the workspace identity/root separately and
   show both when they differ; never silently change directory or attach another repository.
4. Resolve the settings and display a compact receipt. For normal authorized
   work, begin immediately. Ask only for missing required information or a real
   permission boundary. Optional recommendations do not block execution.
5. Show a durable task identifier, stage, and usable result. On completion expose
   changed files, checks, output locations, and any unresolved work.

Illustrative terminal home (values and labels are examples):

```text
UCLI  Work with your connected agents
Workspace  C:\work\weather-app
Ready      Codex / personal       Route  Auto

Recent tasks
  21  Fix forecast refresh       paused        Resume
  20  Review cache changes       complete      Open

Choose  New task | Models | Accounts | Tasks | Workflows | Help
Tip     Type your task below, or Tab to choose an action

> Fix the failing forecast tests
```

Choices are real focusable controls, also reachable through a searchable command
chooser and their CLI equivalent. Search matches natural terms such as "switch
model" and "remaining usage" without changing the canonical names. The chooser
is an accelerator; the visible destinations are the standard route.

## Repeated use and settings continuity

Inside an idle/completed task, label the prompt **Follow up on this task**; while
work is running, label it **Steer this task** and show its delivery checkpoint.
After a result, ordinary follow-ups stay in that task, with a fresh attempt and
completion revision; earlier evidence remains available. **New task** is a visible
action and clears task context only after preserving any draft. Standalone `run`
starts fresh work unless continuation is explicit. **Resume** addresses interrupted
work, not a new goal. A model/runtime change shows what context and required tools
will carry over; unsupported native-session reuse offers a bounded handoff.
The identity and ownership rules are in [contracts](contracts.md#workspaces-and-continuity).

Show Detach only for work already owned by the supervisor. Foreground work may
offer a supported checkpoint and new managed attempt; it cannot imply arbitrary
live process transfer. The UI stays independent of the executor host.

Repeat use should require `ucli`, a prompt, and Enter, or a single `ucli run`
command. Show only exceptions and the compact effective route; expanded
reasoning is available on demand. Cached discovery must not trigger network
checks on every launch. Shell completion covers commands, flags, installed
models, account labels, task IDs, and preset names from local data. Completion
never performs login or spends model usage.

Use the authoritative [contract precedence](contracts.md#effective-configuration):
explicit invocation or intentional in-session change, selected named preset,
trusted workspace configuration, user defaults, then product defaults. Organization
and machine policy bound every layer. Show the winning source in settings detail.
Repository-supplied settings are untrusted input until the
workspace is trusted; they cannot expand permissions or select private accounts
implicitly. Changing a default affects future tasks. Changing a running task
creates a visible pending change applied at the next supported checkpoint.

```text
Task 22  Fix forecast tests                         running
Model    provider/model-name   Effort medium   Account personal
Reason   Localized change; preserves premium capacity
Stage    Running tests        Elapsed 00:19

Actions  Steer | Pause | Detach | Cancel | Details
> Also cover an empty forecast response
Instruction queued; applies after the current test command
```

Task steering acknowledges the exact instruction immediately and records when
it was delivered. Never imply that a model has read a queued message. If a
runtime cannot accept steering, offer a checkpoint and continuation; preserve
the input. Drafts, selected task, search text, and scroll position survive local
view changes. Unsubmitted text remains a draft through picker dismissal, failed
login, failed submission, and navigation back to the prompt. Clear it only after
durable task/instruction acceptance or explicit discard.

Persist drafts locally under the configured history/retention policy; label
unsaved text if persistence fails and keep the in-memory copy usable. On restart,
offer restored text as a draft associated with its workspace/task, never submit
it automatically. A private/no-history session keeps unsubmitted text in memory
only and makes that survival limit visible. Ctrl+C while a draft is present does
not silently erase it; task cancellation and draft discard are separate actions.
Credentials never enter prompt history.

## Accounts and usage without surprises

An account row shows a user-chosen label, provider identity, readiness, eligible
models, observed limits, freshness, and known reset times. Use "unknown" for
missing data, "updated 8 minutes ago" for stale data, and an absolute local reset
time alongside the relative estimate. Reset estimates are not availability
promises. Distinguish account limits, shared organization buckets, model pools,
and local reservations; do not sum incomparable percentages.

Within interactive UCLI, "Use for next task" changes only the current session's
next-task choice. It is visible beside the prompt and cannot be consumed by
another terminal. Consume it only after that session's next task is durably
accepted; failed submission preserves the choice, and later tasks return to
the saved defaults. A standalone account/model picker offers "Start a task with
this choice" or "Save as workspace default" explicitly; it never silently writes
a global one-shot choice. A running task remains bound to its recorded account
until an explicit, supported checkpoint transition. Account details also expose
"Reconnect" and "Remove local connection" with their scope stated. Removal refuses
while active work depends
on the connection or offers to stop those tasks first; it does not revoke the
provider account silently.

Automatic account choice respects user eligibility and provider support. If
capacity is exhausted, show the blocked task and next options: wait for known
reset, choose another authorized route, or cancel. Automatic fallback is allowed
only within the task's recorded policy and budget. Unknown quota can remain
eligible under policy, but is never displayed as unused capacity. A paid API
fallback requires an existing spending authorization.

## Interaction, output, and accessibility contract

| Context | Required behavior |
| --- | --- |
| Interactive terminal | compact home; menus and prompts are available |
| Redirected stdin/stdout or `--no-input` | no chooser, pager, browser launch, or waiting for input |
| `ucli run -` | read prompt from stdin; do not also use stdin for questions |
| Normal command output | result on stdout; progress and diagnostics on stderr |
| `--json` | one versioned result envelope on stdout; no mixed human text |
| Streaming machine output | explicit JSONL event mode with sequence and task IDs |
| `--plain` / `NO_COLOR` / dumb terminal | readable static text; no escape codes or decorative motion |
| `--help` | stdout, exit 0, no discovery network request or agent work |

The native route advertises adapter capabilities before execution and preserves
the native runtime's interaction/output behavior. It does not promise universal
event capture, durable supervision, resumption, or account isolation when the
adapter cannot provide them. Native exit status is returned with route metadata
available separately; do not mislabel it as a UCLI task status.

Commands must not interpret a typo as an AI prompt. `ucli rn` gives a concise
correction. Root/subcommand help comes from the same command schema as parsing
and completion. Follow the user's Help Screen Protocol: examples near the top,
traditional section order, 80-column soft/95-column hard width, and manual links
for explanation. Two deliberate deviations require an implementation decision
record: bare TTY `ucli` includes Tier 0 guidance plus the interactive prompt;
arguments after `ucli native RUNTIME --` belong to the native process, including
its `--help`. Non-TTY bare invocation prints brief help and exits successfully.

Keep input responsive while events stream; preserve selection and scrollback
when output arrives. Never force scroll to the bottom after the user scrolls up.
Tab/Shift-Tab traverse visible actions, arrows traverse lists, Enter activates,
and Escape dismisses a local chooser and restores focus. Search filters locally
without an agent round trip. Ctrl+C uses the task interruption contract below,
not an unexplained mix of dismiss and kill. Paste remains paste; text does not
become commands merely because it contains a slash or newline.

Plain mode is a first-class path for screen readers and remote terminals.
Statuses include words rather than color alone. Check Windows Terminal,
PowerShell, macOS Terminal, and Linux terminals at 80x24 and narrow widths;
mouse interaction is optional. Nonessential spinners can be disabled, and an
animation is never required to accept input or commit state.

## State, feedback, and recovery

| Action/state | Immediate feedback | Preserved state and recovery |
| --- | --- | --- |
| Submit task | durable task ID once accepted | failed persistence leaves draft intact; no agent starts |
| Queue instruction | queued receipt | reveal delivery checkpoint; editing supersedes only undelivered text |
| Pause | pausing, then paused only after checkpoint | keep artifacts; resume adds a new attempt if needed |
| Detach | task ID and reattach command | task continues under a supervisor; otherwise detach unavailable |
| Reattach | existing task and replayed events | attach never starts a second attempt |
| Cancel / Ctrl+C | cancel requested, then confirmed state | preserve partial artifacts; report an uncertain external action |
| Provider failure | specific failing stage and error | resume/retry choices reflect actual checkpoint capability |
| Quota blocked | freshness, known reset, alternatives | retain task; wait or select a policy-eligible route |
| Credential expiry | reconnect named account | preserve task; no global login-file swap |

First Ctrl+C requests cancellation of the currently attached task and waits briefly for
acknowledgment. A second requests its owner to force-stop that attempt within the
owned process scope, never a shared provider daemon; disclose that in-flight
external effects may be unknown. Closing or losing an attached terminal is a
connection loss, not a cancellation request: supervised tasks continue, and
foreground-only runtimes become interrupted/unknown until reconciled. Always
show which survival mode applies before starting work.

Exit codes describe the command's scoped result: `0` success, `2` usage error,
`3` setup/authentication required, `4` no eligible capacity, `5` policy or user
input required, `6` task/validation failed, `7` external outcome unknown,
`8` provider failure, `9` UCLI/persistence failure, `124` deadline exceeded,
`130` user interruption. Detach returning `0` means accepted and durable, not
task completed. Stable structured error codes carry finer detail.

## Acceptance and measurement

These are proposed gates, not measured results. Test first-use discovery with
natural goals before teaching commands; record completion without help,
hesitations, wrong routes, recovery, and time. Start with the user and several
representative CLI users, then revise from observed failures. Compare the same
tasks in a native client and UCLI: launch and submit, choose a model, switch
account, recover quota exhaustion, and find interrupted work.

Targets: a returning user submits ordinary work without opening help; core
choices require no memorized shortcut; automatic selection needs no provider
flag knowledge; a new user identifies effective model/account before submission;
interruption never loses an acknowledged instruction. Treat “prefer UCLI” as
human evidence to collect, not a conclusion from timing tests.

Performance instrumentation separates startup, discovery, route resolution,
spawn/handshake, first provider event, and first useful output. Measure p50/p95
on stated machines and cold/warm caches. The [performance budget table](performance.md#2-proposed-budgets)
is authoritative, including the proposed 250 ms warm interactive-readiness target;
do not apply one latency number to every command. Revise targets only from
baseline measurements and retain the reason. Provider latency is reported
separately. No periodic routing model calls, eager network quota poll, or
token-by-token full-screen redraw belongs on the ordinary input path.

Design inspection completed: visible standard routes, common terms, script
boundaries, settings scope, and interruption outcomes are specified. Runtime,
screen-reader, cross-platform, latency, and actual-user checks remain unexecuted
until the corresponding implementation exists.

## P05 runnable fixture — October 18

The [disposable terminal prototype](../spikes/experience/README.md) now exercises
the common prompt, searchable model/account/effort choices, follow-ups, task
history, draft preservation and simulated recovery. It uses synthetic data only;
it is not the UCLI runtime and performs no provider work. Application/state logic
is separate from the line renderer for later adaptation to the selected core.

The [fixture acceptance receipt](verification.md)
records actual subprocess and filesystem checks, including a terminated owned
process and restart. These checks establish fixture behavior on the recorded
Windows/Python environment. They do not prove provider integration, production
durability, performance targets, screen-reader quality or owner preference.

The [owner walkthrough](verification.md) is prepared
and remains unobserved. This prototype intentionally uses a basic line interface;
multiline paste, live steering, supervisor-backed detach, and recovery of partially
typed characters remain outside the spike. Do not copy those limitations into
the production experience contract as accepted product behavior.
