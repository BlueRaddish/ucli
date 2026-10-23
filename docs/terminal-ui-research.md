# Terminal UI research and implementation priorities

Date: 2026-10-22. Research baseline: UCLI `13653f118fd4`. The later
[owned-worker implementation](owned-execution.md) is separate from that baseline;
its executed results belong in [verification](verification.md). This note is
a technical comparison of inspected UCLI code and fetched primary documentation;
it is not a usability study or a claim that every documented reference feature
is available in the locally installed native clients.

## Current UCLI observations

The shell already has a fixed composer and metadata footer, distinct submitted
prompt bands, typed assistant records, source-relative scroll anchors, slash
completion, searchable selectors, literal paste, and independent plain,
no-color and no-motion modes. Retain these behaviors rather than copying a
different application's appearance or keymap wholesale.

At the baseline, [submission](https://github.com/BlueRaddish/ucli/blob/13653f118fd4eb9df52d4c41812a19bb6c556958/internal/cli/interactive_send.go) saves the
complete composition with its displayed revision, then waits for `runTask`.
The editor/menu loop cannot resume until execution returns. In
[execution](https://github.com/BlueRaddish/ucli/blob/13653f118fd4eb9df52d4c41812a19bb6c556958/internal/app/execution.go), each delta is durable before delivery,
but an output callback failure returns into the driver. The bounded
[renderer](https://github.com/BlueRaddish/ucli/blob/13653f118fd4eb9df52d4c41812a19bb6c556958/internal/cli/render.go) also cancels the invocation when its output
fails. Bounded waiting prevents a permanent stall; it does not separate an
observer's lifetime from the executor's lifetime.

The [Sessions monitor](../internal/cli/sessions_terminal.go) now drains available
100-event pages promptly, preserves a same-task/attempt refresh cursor, and keeps
a bounded 64 KiB output tail. Idle reads retain their two-second cadence. It is
an inspector of durable records, not evidence that another process is alive.
Every successful read still invalidates the wrapped detail cache, even when the
displayed content is unchanged. The main
[transcript](../internal/cli/terminal_transcript.go) also rebuilds its bounded
wrapped rows during view generation. These are concrete optimization seams;
their user-visible cost has not been measured in this research pass.

## Documented reference behavior

| Reference | Relevant documented behavior | UCLI interpretation |
| --- | --- | --- |
| [Claude Code interactive mode][claude] | Enter during work queues input; its delivery depends on the kind of input and current activity. Panels preserve editor state. Transcript navigation and expanded detail have scoped controls. | Preserve composition and explain delivery state. Do not imitate native queue or steering behavior without an implementation that can honor it. |
| [Codex developer commands][codex-commands] | Enter during work injects instructions; Tab queues for the next turn. Slash commands are discoverable and filterable. Background-terminal inspection is separate from stopping those terminals. | The native clients disagree on busy-input semantics. UCLI needs one deliberate contract, not provider-dependent surprise. Inspection and Stop must remain distinct. |
| [Codex CLI customization][codex-customization] | An external editor returns text to the composer before sending. Code blocks and diffs can be syntax-highlighted. | Consider explicit external editing and safer code readability after lifecycle work; neither requires a wholesale shell redesign. |
| [Bubble Tea commands][tea-commands] and [v2 view model][tea-v2] | I/O commands return typed messages; terminal features such as cursor and mouse mode are declared by the view. | Keep the existing Go/Bubble Tea stack. Workers produce observations; the UI reducer owns presentation and focus. |
| [Ratatui event handling][ratatui-events] | Its example separates terminal input, render/tick events and cancellation. | Reuse the separation principle, not the Rust framework or its example's polling frequencies. |
| [Ink accessibility][ink-accessibility] | Screen-reader mode can use descriptive labels and semantic state rather than only the visual rendering. | Treat readable linear output as a first-class mode. Passing keyboard tests or providing `NO_COLOR` does not establish screen-reader compatibility. |

These are live documentation observations, not pinned behavior guarantees for
every native version. The [mouse-capture reference][ratatui-mouse] also notes
backend-dependent behavior. UCLI should retain mouse capture off by default and
complete keyboard routes, preserving ordinary terminal selection and copying.

## Prioritized changes

### P0: owned execution and scoped observation

Separate preparation/acceptance from waiting for completion. Preserve draft CAS
before submission; freeze account, model, effort, permission and workspace at
acceptance. Return an accepted handle so menus and composition remain available.
Refreshing the task revision must follow acceptance rather than wait for the
terminal result. Later edits describe the next input, never mutate a running
attempt's accepted settings.

An observer reads durable events by attempt ID and sequence. A coalesced wake
notification says data may be available; it is not the data, an acknowledgement
or a completion receipt. Slow or closed observers must not fail the driver.
Apply task/attempt identity and view-generation guards before displaying events;
the existing foreground-only reply anchor cannot safely multiplex workers.
Keep bounded pages, cursor continuity, source validation and catch-up fairness.

Use separate operations for closing an inspector, stopping an owned attempt and
exiting UCLI. Retain the user's two-press Ctrl+C protection. An in-process worker
does not survive process exit; shutdown must cancel/join owned work before closing
the store. A stop request is not proof of remote cancellation. Unknown outcomes
retain their existing restrictions.

Busy input remains a draft and is explicitly **not queued or steering** in this
first slice. Enter must preserve it and explain that it was not submitted, rather
than silently schedule another turn. Show owned-live status only from an actual
owned handle; saved records continue to use recorded-state wording. No IPC,
daemon, native-context resume or cross-process control follows from this slice.

### P1: reduce repeated work and improve accessible editing — future

Cache wrapped output by displayed-content identity and width. An unchanged poll,
editor cursor movement or mascot tick should not rebuild unchanged history.
Do not use `readAt` as an invalidation key; it changes on every read. Preserve
source-byte anchors and eviction limits. Optimize incremental wrapping only
after the simpler cache is verified and measured.

Add an explicit external-editor action only with safe temporary-file handling,
terminal restoration, cancellation and returned-text review. Saving the external
file must not send a prompt. Contextual help should describe the current focus
and available action rather than add competing global shortcuts.

Keep severity labels and focus indicators meaningful without color. Preserve
static motion settings and provide quiet, linear progress in plain mode. Check
actual screen-reader behavior separately; Ink's features do not imply that
Bubble Tea or UCLI inherits equivalent support.

### P2: richer content, only from real structured data — future

Improve code/diff readability without changing copied text or interpreting
provider output as terminal controls. Group tool activity only when adapters
provide verified typed events. A textual model claim such as “running tests”
must not become a fabricated tool-execution status. Keep account/model
availability and errors discoverable without expanding the fixed chrome.

## Verification and packaged-test boundary

Extend the existing Enter/CAS, output-stall, interruption, transcript-anchor and
Sessions tests. Required consuming checks include observer failure while a
worker completes durably; view switching without foreign output or duplicates;
owned Stop and shutdown; busy Enter preserving unsent text and pins; and cursor
catch-up across cancellation and errors. Cache checks should count actual rebuilds
for unchanged reads versus delta, warning, state and width changes.

The existing Muse protocol child fixture can emit output and wait for an
interrupt, making deterministic ownership tests possible. It cannot simply be
substituted into the shipped executable: [Muse admission](../internal/app/routing.go)
requires the pinned genuine binary hash. Keep that check intact. Real Muse echo
does not implement the fixture's hold commands and may finish before a human UI
action can be observed.

Use App/worker tests with the controlled driver and actual hidden protocol child
for long-running lifecycle proof. Use the shipped executable under the existing
ConPTY harness for ordinary asynchronous demo submission, menus, draft retention
and durable completion. If a test-only compiled UI harness is needed for a
deterministically held worker, label that evidence separately from shipped-binary
acceptance. Do not add a production environment bypass or artificial demo delay
solely to make the test pass. A seeded recorded-running task proves inspection,
not ownership of a live executor.

This research pass ran no Go jobs, native clients, sign-ins, inference or
downloads. Implementation and host-specific results belong in
[verification](verification.md); human usability and screen-reader validation
remain unverified.

[claude]: https://code.claude.com/docs/en/interactive-mode
[codex-commands]: https://learn.chatgpt.com/docs/developer-commands?surface=cli
[codex-customization]: https://learn.chatgpt.com/docs/cli-customization
[tea-commands]: https://github.com/charmbracelet/bubbletea/blob/main/tutorials/commands/README.md
[tea-v2]: https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md
[ratatui-events]: https://ratatui.rs/recipes/apps/terminal-and-event-handler/
[ratatui-mouse]: https://ratatui.rs/concepts/backends/mouse-capture/
[ink-accessibility]: https://github.com/vadimdemedes/ink#screen-reader-support
