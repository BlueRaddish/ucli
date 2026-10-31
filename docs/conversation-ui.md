# Conversation interface contract

The conversation is the main surface. Keep one full-width reading path, a fixed
header, and a framed composer at the bottom. Model, effort, account, permissions,
location and reported usage belong below the composer. Views for models, saved
sessions and workflows replace the body while
retaining the draft and its editing position. These requirements describe the
approved direction; the verification guide and current module READMEs identify
which paths have executed evidence.

## Display records

Keep user, assistant and system records structurally distinct. A user prompt has
a shallow band, one right-pointing marker and a short caption; wrapped prompt
lines are indented. Assistant text uses the normal
background with a concise source heading. Color supplements those distinctions.
Do not infer roles from the text, and do not interpret an assistant's headings as
UCLI controls or status.

Build prompt records from the submitted composition and restored attempt input.
Build assistant records from the corresponding attempt's deltas and saved output.
Append streaming fragments to the same record without inserting newlines or a
new heading per fragment. Keep code indentation and fenced text literal. Tool
summaries require actual typed tool evidence; do not invent checks from prose.
System diagnostics remain separate from the provider's response.

The visible transcript is a bounded projection, not the durable result store.
Records need stable identities and reading anchors. Pruning must disclose that
older display content was omitted and remove jump actions whose targets are no
longer retained. Saved results remain available through the existing history path.

## Navigation and composition

When the latest retained user prompt is above the viewport, show a compact
excerpt and a prompt-jump action. `Ctrl+Up` is the keyboard route. Jumping reveals
the prompt's caption after the conditional strip disappears.

Follow incoming output at the tail. Scrolling up pauses following, not execution;
preserve the reader's text anchor across new fragments and resize. A separate
Latest output action and `Ctrl+End` return to the tail and resume following.
Neither action moves the composer cursor or changes the draft. Navigation rows
consume body space; they must not cover output or move the footer.

Retain Enter-to-send, empty Enter as a no-op, literal paste, newline keys, slash
completion before invocation, and the two-press Ctrl+C safeguard. Opening an
action menu with an ordinary draft must navigate without submitting that draft.
Escape returns one level. Selection preview is separate from a committed model,
account, effort or permission pin. Cancel preserves every independent pin.

The rich terminal supports clicks on visible controls and menu choices. Shift-drag
retains the terminal's copy gesture where supported; F2 disables mouse reporting
for native selection. F2 can enable it again. A browser mockup's clickable controls
do not establish terminal mouse or copy behavior. Keep every core action available
by keyboard. `NO_COLOR` retains
the same geometry and role labels; reduced motion changes no logical state.

## Model and permission selection - October 28

`/models` narrows choices through CLI, model, then effort. Each layer names its
scope and keeps the current choice visible. Cached runtime/model metadata supplies
the lists; browsing must not run a native CLI, refresh authentication, or submit a
prompt. Auto remains available. An explicit model offers its advertised effort
levels, with honest handling of fixed or unknown capabilities.

Model and effort are tentative until the final choice. Commit both together,
preserving the account, permission, and unsent composition. Escape/Back returns
one layer; abandoning the root leaves all pins unchanged. In the rich terminal,
returning to a layer restores its query and focused row. Keep `/effort` available
as a direct action for repeated changes.

The permission picker offers modes the current application path can admit.
Native routes currently implement read-only and sandbox (full local access);
the local demonstration implements read-only. Stored `workspace-write` and
`unrestricted` vocabulary does not establish native support. Restored unsupported
choices require an explicit supported selection before sending; cancellation
preserves the draft and old choice. A rich send with an unsupported restored pin
opens that picker and returns to the saved composition; a later Enter sends.
Never silently promote a permission or
weaken adapter checks to clear a routing error.

## Readable work and navigation - October 30

The chat loop is Enter -> visible preparation -> accepted work -> observed client
start -> response fragments -> durably saved outcome. These labels describe real
boundaries, not a timed animation or an estimate of remote completion. Client
start means a local owned process started; it does not establish provider
acceptance, ongoing liveness or private reasoning access. There is no separate
Saving phase unless persistence actually exposes one.

Keep the current job in a stable progress row. Clicking it or a scoped response
heading opens a read-only job inspector; F3 is the keyboard route. The inspector
shows the captured route, recent UCLI activity, and received response. It must
open even while synchronous preparation is running. Escape returns to the same
draft/view; inspecting cannot send, cancel, sign in, change pins or reveal native
credential/context payloads. Saved sessions remain a separate history view.

Speaker captions, a two-cell content gutter and compact inter-message gaps
separate user, assistant and UCLI messages. Wrap prose at word boundaries; keep
paragraphs, lists, fences and relative code indentation literal. Streaming chunks
are not paragraphs. Preserve source-byte anchors across wrapping and resize.

The idle footer shows only `← agents · ? shortcuts`. Left and `?` open their menus
at empty input; left-arrow editing and question marks inside real prompts stay
literal. F1 opens shortcuts while a draft is present. Slash completion and Tab
remain available; the shortcuts menu explains Enter, newline, copy, navigation,
mouse, sessions and stop. Footer metadata uses semantic colors, retains readable
monochrome text, and says `Usage not reported` when no provider measurement exists.
Browsing never manufactures an allowance, token count or reset timer.

Login menus distinguish using an existing sign-in from opening provider sign-in,
show the last observed connection state, and keep technical details secondary.
Workflow inspection remains an offline plan viewer, not workflow execution.

## Layout budget

The existing shell uses three header rows at ordinary sizes. The baseline is one
input row; multiline composition grows upward while its last row and the footer
remain fixed. Rows below are zero-based.

| Region | 100 columns × 30 rows | 60 columns × 20 rows |
|---|---:|---:|
| Header and rule | 0–2 | 0–2 |
| Job progress | 3 | 3 |
| Body, including conditional navigation | 4–21 | 4–11 |
| Notice | 22 | 12 |
| Upper composer rule | 23 | 13 |
| Input | 24 | 14 |
| Lower composer rule | 25 | 15 |
| Model/effort/usage, location, permissions/account | 26–28 | 16–18 |
| Keyboard hints | 29 | 19 |

Shorten secondary metadata at narrow widths. At tiny heights remove decoration
before essential input, status and controls. Wrap using grapheme/display widths,
escape terminal control characters, and retain literal durable text for copying
or inspecting outside the bounded view.

## Native catalogs and session boundaries

Browse cached model metadata without starting a native process or downloading
anything. Refresh is explicit and bounded. Prefer native structured discovery,
retain native model identifiers and advertised efforts, and keep last-good data
when refresh fails. Display stale or unknown evidence honestly. A model catalog,
an authenticated account, execution compatibility and remaining capacity are
separate observations. Native updates must not silently replace account identity,
clear a draft, change an exact pin or imply a logout.

UCLI owns accepted submissions independently of rich-terminal observation within
the same process. Editing, switching tasks and inspecting sessions stay available
while workers run. Enter on a busy task saves the next composition without
queueing, steering or resubmission. `/stop` requests cancellation only for the
current task's owned handle; its terminal result still comes from the runtime.
Closing UCLI requests stop for its owned workers. Saved UCLI task history does
not provide native conversation memory: Codex and Claude submissions start fresh
contexts containing the new input. The read-only sessions inspector itself does
not control workers. See [owned execution](owned-execution.md).
Workflow inspection is not workflow execution. Do not label these capabilities
as persistent background conversations until implemented and independently tested.

The later process supervisor must own native conversation handles scoped to provider,
account, workspace and permissions; the UI subscribes to durable sequenced events.
Launch workers lazily, allow one active turn per native context, and reuse native
resume only where verified. UI detachment or a slow reader must not imply a stop.
Cancellation and process exit require a matching native terminal receipt to settle
remote work. Unknown outcomes must remain fenced against automatic resend.

## Verification and references

Follow [the verification route](verification.md) and
[the hidden console guide](../internal/cli/conpty.md). Exercise submitted and
restored prompts, fragmented replies, role-spoofing text, literal code, pruned
history, paused streaming, prompt/tail navigation, resize, menus, cancellation,
multiline drafts and monochrome. Use the exact packaged executable at both normal
and narrow sizes. Browser layouts, cross-builds and screenshots have their own
scope; none establishes native provider or human usability results.

[Claude Code fullscreen](https://code.claude.com/docs/en/fullscreen) provides the
reference for fixed input, conditional prompt context and paused following.
[Command Line Interface Guidelines](https://clig.dev/) informs discoverability,
consistent language and concise output; it does not prescribe fullscreen layouts.
The [Bubble Tea chat example](https://github.com/charmbracelet/bubbletea/tree/main/examples/chat)
illustrates viewport/editor composition. Preserve UCLI's draft, permissions and
interruption contracts when adapting those patterns.
