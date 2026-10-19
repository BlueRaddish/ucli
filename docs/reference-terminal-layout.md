# Terminal layout reference and implementation contract

Reviewed October 19, 2026 against UCLI baseline `70afc63`. This is a source-backed
design contract, not proof that the new UCLI layout has been implemented or tested.
The user's requirement is authoritative: keep version, location and selected
model at the top; keep the composer at the bottom; place relevant notifications
immediately above input; distinguish errors, warnings and ordinary content.
Ease of use means that those facts and the next action remain visible without
remembering commands or reopening menus.

## Evidence and current native versions

The installed files changed after the earlier native-client audit. This review
read program bytes and installation metadata only; it launched neither client,
read no native account/history stores and made no inference calls.

| Program | Current identity | Evidence scope |
| --- | --- | --- |
| Claude Code | Windows file version `2.1.303.0`; 290,228,384 bytes; SHA-256 `1787c60619bad78543c67e4d9d8a868869c35a904f2a85eb9ea46c4808af196b` | Current executable hash/metadata and limited readable bundle inspection. The Footer key table is at byte 246892789 and separates navigation, open and clear-selection actions. No current interactive screen was reached. |
| Codex | npm package `0.163.0`; vendor executable 341,270,320 bytes; SHA-256 `2692c8c7bb2cf8b0aefe638741fc90236f4b824c59578ce84994f9de58200993` | Current installed hash/package metadata and official `rust-v0.163.0` source. The package version identifies the reviewed tag; this is not an attestation that every binary byte derives from that source. No current interactive screen was reached. |

The existing [Claude audit](reference-claude.md) concerns **2.1.299**, including
its exact source offsets and earlier isolated ConPTY observations. Its startup
probe stopped at a connectivity boundary; it did not observe authenticated
header/composer behavior. Do not relabel those observations as current-version
evidence. Do not widen native execution admission merely because a newer UI was
reviewed.

Current [OpenAI CLI documentation](https://learn.chatgpt.com/docs/codex/cli)
illustrates a welcome card with model and directory, followed by input and a
context/shortcut footer. The [developer commands reference](https://learn.chatgpt.com/docs/developer-commands?surface=cli)
documents configurable footer fields including model/reasoning, directory and
version. Neither establishes that the welcome card stays at the top during a
long conversation.

Claude's [status-line documentation](https://code.claude.com/docs/en/statusline)
places its configurable status line at the bottom and says it may temporarily
hide during some interactions. Its [interactive-mode documentation](https://code.claude.com/docs/en/interactive-mode)
distinguishes closing a dialog, interrupting execution and clearing a footer
selection. These are useful distinctions; UCLI retains its own confirmed input
and cancellation semantics. No status-line shell script or native configuration
is needed for the UCLI design.

## Source-derived patterns worth keeping

The exact Codex tag provides a useful separation of responsibilities:

- [Bottom pane](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/tui/src/bottom_pane/mod.rs#L1):
  the composer and transient views share an input owner; the parent decides
  higher-level interrupt/quit behavior. Lines 19–22 place banners and activity
  hints above the composer and protect normal draft/paste routing.
- [Composer allocation](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/tui/src/bottom_pane/chat_composer/composer_layout.rs#L39):
  preserve input and hints before allocating a persistent status row; derive
  positions from the available rectangle rather than adding unbounded rows.
- [Warning notice](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/tui/src/bottom_pane/chat_composer/warning_notice.rs#L1):
  warnings do not change composer geometry; interactive hints take precedence;
  the warning count/label receives the accent. Narrow widths reduce the notice
  rather than overlapping another control.
- [Color semantics](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/tui/src/style.rs#L39):
  failures use red; attention avoids yellow on a light or unknown background.
  The passive amber warning style at lines 188–201 adapts to the background and
  removes dim styling. This is evidence for readable semantic treatment, not a
  requirement to copy its theme-detection system.
- [Native rendering](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/tui/src/tui.rs#L466):
  the normal initialization is an inline viewport with terminal scrollback.
  UCLI's fixed top header therefore follows the user's request, not a claim of
  exact native imitation.

Fetched full-file SHA-256 identities, in the order above:

```text
bottom_pane/mod.rs                    1259694cd13b5d8641a3615cfd1b89eecd608bf63ed0a59ca500c6b5b668b610
chat_composer/composer_layout.rs     0d481f30e9aaeec083e0b9dbce4b8e0883a012c1fa918e2a49dbeb1cc03e2f07
chat_composer/warning_notice.rs      d66106d040b80e99150ec58bba27750c07fa9e1b855f550a97faafc858f3dfef
style.rs                            ea425b86ed801ff73f3e6f3d1d3eb4f99b3820f9d8509f2d40da539021884f10
tui.rs                              d32177613b8399a78e5a36d388052bae70f63a5a2fd425df510ea4dadf993261
```

## Proposed UCLI layout and ownership

Use one shared rich-session shell, with stable regions:

```text
UCLI <version>       <selected task location>
Model <requested model> · effort <intent> · <permission>
──────────────────────────────────────────────────────
Transcript or active task/menu/workflow view
… bounded viewport; saved results remain accessible …
Warning: <actionable message; work is preserved>
> <editable composition and cursor>
Enter save · /send submit · Tab actions · Ctrl+C exit
```

The header should derive from session state: selected task workspace when a task
is open, otherwise the session directory. Show the requested model as requested;
do not label `auto`, a cached catalog or a native acknowledgment as proof of a
successful effective model. Update header values after a confirmed selection or
task change, without triggering provider discovery, quota refresh or disk work
from rendering. Retain an explicit permission label, especially for full local
access. Never replace it with an ambiguous decorative badge.

At the reviewed baseline, [terminal_ui.go](../internal/cli/terminal_ui.go) renders
only the current widget and incomplete stream fragment. `terminalWriter` sends
completed lines through `tea.Println` outside that view. That can preserve shell
scrollback, but it cannot by itself promise a fixed top header as output grows.
The smallest coherent change is a shell-owned, bounded transcript viewport for
the rich session, reusing the existing terminal framework. Do not combine direct
scrollback writes with an independent sticky header. Standalone plain/JSON
commands should retain their existing output path.

Keep the existing editor and picker/workflow reducers. They supply body content
and a local cursor; the shared shell owns region allocation, styling and one
cursor-coordinate translation. Give widgets the actual remaining rectangle so
their row budgets do not count header/composer space twice. In a focused menu,
keep the original composition visible when space permits but route keys only to
the active widget. Escape returns to the same composition, cursor, task, revision,
query and committed selection. Rendering, resizing and color changes must never
save drafts or change routes.

Use typed presentation roles such as error, warning, success, progress and muted
context at the existing output/event boundary. Do not infer severity by parsing
arbitrary transcript words. A notice is a view of a real outcome, not a second
task-state authority. Keep actionable failures reachable in transcript/results;
an ephemeral notice must not be their only copy. A newer success acknowledgment
must not erase an unresolved blocking error. Do not add automatic retries,
provider fallback or fabricated progress to make the status area look active.

## Width, height, color and latency contract

These are proposed allocations to test, not measured native behavior:

| Constraint | Required behavior |
| --- | --- |
| 80×24 and 120×28 | Header, body, notice area and input remain separate while transcript grows. A multiline composer grows upward within its bound. |
| 60×20 | Shorten path and less important context, preserving model/permission meaning, input and applicable keys. Do not wrap header fields into the transcript. |
| 40×8 and 30×6 | Compact chrome first. Keep input/cursor and the highest-priority actionable notice visible; show the active menu's close/confirm route. Content can scroll or use a compact view. |
| Extremely small height | Use a deterministic compact fallback; never draw a cursor outside the viewport or discard text. There may be insufficient rows to show every header field simultaneously. Restore those fields on growth. |
| Long paths, CJK, combining marks, emoji | Fit by terminal cells/graphemes using existing helpers. Elide path middles where useful; preserve the actual stored path. No byte-count slicing or ANSI-dependent width estimates. |
| Error/warning | Pair red/yellow or amber with explicit `Error`/`Warning` text and a recovery action. Do not dim consequential feedback. Verify the actual palette on light and dark terminals. |
| `NO_COLOR`, plain, redirected output | Preserve state labels and recovery text without color. Avoid escape sequences in plain/JSON output. Keep the project's existing rich/plain selection policy explicit. |

Sanitize external text before applying trusted styling. Retain the existing
last-column margin to avoid terminal autowrap, or remove it only with actual
emulator proof. Centralize cursor mapping and clear stale cells after shrinking
content or resizing. Never let output introduce cursor/OSC sequences. Respect
the existing bounded input reader, borrowed-handle ownership and shutdown deadline;
restore cursor visibility and terminal modes on normal exit, cancellation and
render failure. Preserve final diagnostics outside an alternate screen if that
screen mode is chosen.

The renderer should depend on visible rows and changed content, not total saved
history. A bounded transcript cache is a display optimization, not durable
storage: indicate truncation and retain the existing result/history route.
Coalesce streaming repaint work without dropping output or changing persistence.
Do not poll providers or create a background timer merely for static header
fields. Compare packaged first frame, menu opening and typing feedback with the
existing measurements; no native-parity claim follows from source review.

## Consuming verification for the implementation

Reuse the project's [verification route](verification.md), current hidden ConPTY
checks and terminal image renderer; add no second UI harness. Retain binary hash,
dimensions, key sequence, raw terminal bytes and the observed-frame boundary.

1. Start with a nonempty Unicode draft. Open a picker, search, resize and cancel;
   assert identical text, cursor, selected task, revision and route afterward.
2. Stream enough synthetic lines to exceed the screen, including a long line
   without a newline. Header and input stay anchored; scrolling old content does
   not move focus or silently jump back on every chunk.
3. Trigger real fixture warning/error/success events. Notice placement and labels
   agree with their meaning; complete message and recovery remain accessible.
4. Exercise the width/height matrix above and light/dark/NO_COLOR modes. Inspect
   captured frames for wrapping, clipped keys, stale cells and invisible text.
5. Interrupt, close a menu, fail output and restart independently. Confirm draft
   preservation, no surprise dispatch, restored terminal ownership and actionable
   final diagnostics. Closing a view must not pretend execution was cancelled.

This audit establishes reference facts and implementation checks only. New-shell
runtime correctness, performance, visual contrast and human comfort remain to be
verified against the actual packaged implementation. Earlier provider execution
limitations remain unchanged.
