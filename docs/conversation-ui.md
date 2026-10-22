# Conversation interface contract

The conversation is the main surface. Keep one full-width reading path, a fixed
header, and a framed composer at the bottom. Model and location belong below the
composer. Views for models, saved sessions and workflows replace the body while
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

Terminal mouse reporting stays off by default so native terminal selection still
works. A browser mockup's clickable controls do not establish terminal mouse or
copy behavior. Keep every core action available by keyboard. `NO_COLOR` retains
the same geometry and role labels; reduced motion changes no logical state.

## Layout budget

The existing shell uses three header rows at ordinary sizes. The baseline is one
input row; multiline composition grows upward while its last row and the footer
remain fixed. Rows below are zero-based.

| Region | 100 columns × 30 rows | 60 columns × 20 rows |
|---|---:|---:|
| Header and rule | 0–2 | 0–2 |
| Body, including conditional navigation | 3–23 | 3–13 |
| Notice | 24 | 14 |
| Upper composer rule | 25 | 15 |
| Input | 26 | 16 |
| Lower composer rule | 27 | 17 |
| Model and location | 28 | 18 |
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

UCLI currently owns foreground native submissions. Saved UCLI task history does
not provide native conversation memory: Codex and Claude submissions start fresh
contexts containing the new input. Saved session inspection is not worker control.
Workflow inspection is not workflow execution. Do not label these capabilities
as persistent background conversations until implemented and independently tested.

The later supervisor must own native conversation handles scoped to provider,
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
