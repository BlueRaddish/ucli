# Experimental terminal surface

The `internal/cli` package implements UCLI's command-line interface and interactive terminal shell, delegating command execution, settings resolution, and state persistence to `internal/app`. For deterministic local evaluation, `--demo` or `/demo` supplies simulated local responses, while an explicit `muse/echo` pin exercises a Windows no-auth echo path. Native account linking and request dispatch for Codex and Claude remain experimental and require reviewed native builds plus fresh account and configuration checks; reported native status does not prove account entitlement or live execution capability. Task pins persist across turns on the active task, whereas `/new` resets a new task to product defaults. Every native dispatch starts a fresh provider session: UCLI makes no promise of native session resumption, prior-context carry-over, or cross-provider handoff, and native reconciliation is unavailable.

`doctor` deliberately scans the full local SQLite state with `CheckIntegrity`
before reporting success. Its JSON retains `local_state:"opened"` and adds
`integrity:"ok"` and `state_schema` only after the scan finishes; human output says the full integrity
check passed. Failure returns the typed storage error without a healthy report.
Normal catalog/task queries retain startup and record checks, but do not certify
the integrity of the entire archive. Help, version and offline workflow checking
still avoid opening the app or database.

`ucli backup FILE` saves a standalone SQLite snapshot of the selected UCLI home,
including committed task data still in the live write-ahead log. Relative paths
resolve from the current directory. The destination must be a new local file
outside the live state directory, in an existing parent directory; existing files
are never overwritten. The command does not read stdin or accept task/provider
flags. Human output identifies the file, SHA-256 hash, size and task/evidence counts.
`--json` returns a `backup` envelope with the versioned backend receipt containing
`version,schema,path,sha256,bytes,created_at,tasks,attempts,events,unsettled_attempts`
and optional `warnings`. Failure returns an error without a success receipt.
The snapshot contains saved prompts, results and drafts; native credential stores
are not included. Running/uncertain attempts remain unsettled in the copy. This
preserves local data; it adds no restore or automatic resume action, and does not
certify physical power-loss durability.

`ucli migrate BACKUP_FILE` explicitly upgrades a quiescent schema-1 home to schema2
task metadata. The backend retains the SQLite writer barrier through verified
backup, snapshot certificate, backfill/audit and commit. Opening homes does not
upgrade them. Running/unknown attempts or leases block the upgrade. The command
returns a `migration` envelope even on failure, preserving any published backup
and `applied`, `not_applied`, `unknown` or `unchanged` outcome. The certificate
describes the snapshot only. An unknown commit requires inspection with doctor;
neither a failed command nor its certificate establishes rollback. Older builds
supporting schema1 only refuse an upgraded home. See the state package contract.

`ucli workflow check FILE` is a separate offline path. It validates version 1 JSON
and shows an initial dependency plan with every step pending. Use `-` for redirected
stdin and `--json` for the `workflow_plan` envelope containing
`{spec_id,offline:true,dispatched:false,plan}`. It neither opens the app/store nor
accepts, executes or persists tasks, including on errors; `--home` is ignored.
Execution/selection flags are rejected. The human summary lists ready and blocked
step IDs with reasons without reproducing prompts. This verifies schema and graph
structure only, not model/account availability, permissions or runtime execution.
The parser bounds file size, steps, edges and aggregate prompt bytes. File/JSON/
graph errors return typed exit 2. Interrupted input returns 130 (deadline 124).
Caller-owned redirected input shares the one blocked-reader ceiling described below.
The [workflow package reference](../workflow/README.md) has a copyable example.

Build and run from the repository root using the documented `tools/dev.py` route,
or use the installed Go executable explicitly:

```console
go test ./internal/cli
go run ./cmd/ucli --help
go run ./cmd/ucli --home .build/cli-tryout run --demo "Try a task"
go run ./cmd/ucli --home .build/cli-tryout
```

Running `ucli` with no arguments on a real terminal opens the interactive shell, while redirected invocation prints help and exits. The rich layout has a cat/version header, Chat/Sessions/Connect navigation, a job row, and a bounded scrollable body above a framed input. A notice sits above the input; model, directory and other metadata sit below it with compact navigation hints. Tiny terminals reduce decoration. Interactive selectors, inspection-only workflow views (with no executor), voice controls, and login prompts share the shell, and standalone panels retain real controls rather than a pretend editable draft.

Submitted prompts render with a full-width highlight, an indented `Your prompt` caption, and a single `>` marker on the first line of text. Wrapped and subsequent lines indent without a vertical gutter. Monochrome output keeps the same layout. Assistant replies retain literal code.

Assistant deltas extend an explicitly typed reply rather than inferring roles from
prose. When loading saved results, the transcript derives roles directly from the
recorded attempt inputs and outputs. Markdown, code fences, indentation, and
newlines are rendered literally without introducing a new Markdown engine or
synthesized tool-event groups. Transient status updates appear in the notice row,
whereas warnings and errors remain retained in the transcript.

When the latest retained prompt heading scrolls above the viewport body, `Latest
prompt` (`Ctrl+Up`) reveals it, pauses auto-following, and leaves the composer
intact. `PageUp` and `PageDown` preserve source-text anchors across incoming
output and terminal resizes, while `Latest output` (`Ctrl+End`) resumes following.
Open menus capture keyboard input. Visible controls and choices accept pointer
input; F2 toggles mouse reporting and `--no-mouse` starts with it disabled for
native terminal selection. Shift-drag remains available where the terminal
supports bypassing mouse reporting. Keyboard navigation remains complete.

Replies and system messages use a two-cell reading gutter with separate speaker
captions and inter-message spacing. Prose wraps at word boundaries; literal
paragraphs, lists, fences and relative code indentation retain their source text.
All model, effort, usage, directory, permission and account metadata sit below
the framed input. The idle hint is `← agents · ? shortcuts`; Left and `?` open
menus only at empty input, while F1 works with a draft. Usage says `not reported`
when no provider measurement exists.

A stable job row exposes preparation, acceptance, observed local child start,
response and saved outcome. Click that row or a matching retained reply heading,
or press F3, for a read-only inspector. It opens during synchronous preparation,
keeps received output and bounded activity visible, and consumes typing/paste
without changing the hidden composer. Escape restores the prior view. Inspector
activity is display state, not proof of remote liveness or private reasoning;
saved results and session history remain the durable inspection route.

The in-memory display is bounded to 64 KiB of source text, 400 records, and 600
wrapped rows. When older content is evicted, the transcript discloses the eviction
and removes evicted headings as jump targets, though `/result` still reads the
full saved output separately. Non-demo chrome labels the session as fresh native
context to make clear that displayed history does not imply provider memory.

Model browsing and route explanations remain offline. When a submitted prompt cannot auto-route solely because the model catalog is stale, UCLI runs one bounded metadata-refresh pass across eligible linked accounts and replans without submitting or retrying AI work. Independent account, model, effort, and permission pins stay intact. If the refresh fails, the prompt is preserved, and stale-catalog errors no longer append unrelated full-local-access permission advice.

When slash completion is not open, pressing `Tab` opens the action discovery menu while keeping the committed selection separate from transient menu focus: type to filter, use wrapping `Up` and `Down` keys to move focus, press `Enter` to confirm, or press `Escape` to cancel while preserving in-progress composition.

In the rich editor, `Enter` sends one nonempty composition without repeated auto-sends, while `Alt+Enter`, `Shift+Enter`, or `Ctrl+J` inserts a newline and `/send` remains supported. UCLI persists the full pending composition before routing or acceptance, leaving recoverable text if a missing account, unsupported version, or conflict stops submission; compare-and-swap conflicts save a separate recovery draft. Restored drafts load into the editor for editing, and later rich saves replace the draft instead of appending duplicates. Changing tasks or settings preserves pending text, and bracketed paste or voice input stays literal until `Enter`. Input is bounded to 1 MiB with Unicode grapheme editing and `EOF` text preservation without submitting, without per-keystroke disk autosave or crash recovery for undelivered keystrokes.

`App.Start` completes admission and durable acceptance synchronously, then returns a handle to a host-owned in-process worker. While work runs, the rich UI can edit, browse menus, and inspect other saved work. Durable events observed through `Follow` are scoped by task and attempt identities so concurrent responses do not merge. Submitting again on a busy task saves the composition without queuing, steering, or running it. `/stop` targets only the selected task's current handle owned by this process, never an arbitrary recorded running row. Normal close and confirmed `Ctrl+C` request stop and join owned work, though a stop request does not prove remote cancellation. There is no detached broker, survival across terminal closure, or persistent native conversation. See the [terminal UI research](../../docs/terminal-ui-research.md) for source comparisons and follow-up design work.

During idle editing or active rich operations, the first `Ctrl+C` shows a two-second confirmation notice without canceling work or erasing text. A second `Ctrl+C` within that window confirms interruption or exit while keeping processed unsent text; typing or pressing `Esc` disarms the notice, and timeout restores the latest notice. Noninteractive `SIGINT` still cancels immediately. Interruption does not prove remote cancellation. Uncertain native outcomes retain their guard, with no automatic retry or resume.

The `/models` view leads with friendly names rather than raw model IDs and keeps model, account, effort, and permission pins independent. Setup, info, cancel, and refresh actions never silently alter any pin. In interactive `/login`, the default Use account already signed in on this computer option only checks and reuses local state, while a separate Sign in action starts a supported native browser or device flow. Binary installation, cached sign-in, version compatibility, and execution support are tracked separately—authentication or catalog data never proves entitlement or execution—with plain-English next steps, AGY models shown as unavailable, and GitHub as identity-only.

Running `/sessions` opens a read-only overview where you can inspect session details and choose Back while keeping your search query, focus, active draft, pins, and revision across refreshes. It displays recorded task and attempt status plus a bounded saved event and output feed; a recorded running status does not prove the owner process is still alive, and unknown outcomes stay unknown. The view never runs task graphs or dependencies, dispatches, switches the active task, cancels, detaches, resumes natively, or recovers uncertain work. Use `/tasks` to open earlier work for follow-up, where native follow-ups start fresh sessions using only your new input without automatic conversation handoff.

In the rich composer, typing `/` at the beginning of a single-line, command-only input opens a navigable command list with descriptions in the body area above the input, keeping input, footer, and cursor coordinates stable. A prefix such as `/mo` narrows the list, `Up` and `Down` wrap through matches, and a zero-match state remains visible while the input stays editable. `Tab` always completes a selected suggestion; `Enter` completes a partial command. Completion inserts the canonical command plus a trailing space without executing it, and a subsequent ordinary `Enter` invokes the command. Fully typed commands retain their usual `Enter` behavior. `Escape` dismisses the list without changing the text. Completion does not activate for ordinary text containing slashes, arguments after the command, multiline input, or literal bracketed paste, and it leaves the separate `Tab` action menu, the plain interface, and literal paste protection unchanged.

Display flags act independently: `--no-color` and `NO_COLOR` disable color while keeping rich keyboard handling, `--no-motion` keeps static cat poses, and `--ascii` uses ASCII cat glyph fallbacks without sanitizing prompts or output to ASCII. Setting `--plain` or `TERM=dumb` selects the static line interface. Because multiline plain pastes with leading slashes can invoke slash actions, use `ucli run -` for arbitrary redirected text or `/draft TEXT` for a single slash-prefixed line. The `/result` command reads saved task output rather than transient help or display history, and human usability remains unproven.

The most recent saved draft restores in the same execution directory, even if
more than 100 newer tasks exist elsewhere. Opening another task shows
its directory and makes it the execution directory for that thread. Searchable
numbered choices require no shortcut memory and retain the draft. A task with no
provider route remains a draft; changing a setting and sending continues the
same task. `/result` inspects the latest five attempts without creating an attempt;
the visible `/older` action pages backward, and `/result` returns to recent work.
History navigation preserves the selected task, unsent draft, settings and displayed
revision used for concurrency checks. `/new` and explicit task selection reset the
history cursor. `/tasks` searches the entire archive through bounded summary pages;
it never loads draft bodies for the menu. Its visible `/older`, `/recent` and Back
actions retain the search and page on reopening. Enter clears the search;
`/search TEXT` can search literal numeric text or words used by menu actions.
Browsing preserves the active task, draft, pins and displayed concurrency revision.
Only choosing a numbered task changes that context. Invalid search input leaves the
last valid search usable, and EOF inside the finder never saves search text as a
draft or submits work. `/recover`
only reconciles a stopped local demo; it never retries or replays provider work.

Draft saves include the revision actually displayed. A concurrent edit preserves
the submitted input in a separate recovery draft, leaves the newer task untouched,
and keeps the terminal open. The retained unsent view is visibly blocked from
editing or sending until `/tasks` explicitly selects a task or `/new` starts fresh.
`/result` and `/help` remain usable. No conflict silently switches task context.

Rendering uses native terminal colors across the fixed header, footer, and bounded body, with at most one model-owned animation clock scheduling the next actual frame deadline. Animation follows real operation state rather than generic `Enter` or success labels: `wave` and `done` use `U+2A4A` for a friendly mouth (mapped to an underscore in ASCII mode) while `working`, `looking`, `paw`, and `listen` use an underscore when focused or calm, errors and warnings use static feedback, voice recording stays static rather than faking audio activity, and the completed-task gesture requires a durably persisted result. The `Rest` catalog frames also pair closed `-` eyes with the `U+2A4A` friendly mouth while retaining their `z`/`Z` motion, timings, and padding, but `Rest` still has no idle runtime hook. Saving, cancellation, and focus never wait for animations to finish, and plain or JSON output never animates. Live in-flight steering and session detach remain unavailable, and the terminal library restores terminal mode on exit.

Windows uses an owned, cancellable record-to-VT input adapter because
the pinned library's native VT path mishandled Alt+numpad Unicode and modifier NULs
in actual ConPTY. It retains Unicode committed on Alt release, skips modifier-only
events, preserves meaningful control keys, joins UTF-16 pairs across reads and
does not close the borrowed console handle. Hidden packaged tests cover the loop;
they do not establish behavior in every physical console/keyboard/IME combination.
The pinned VT library drops a literal U+FFFD from paste. The Windows adapter rejects
that character explicitly and points to redirected `ucli run -`, preserving the
input boundary rather than accepting changed text. Removing this ceiling requires
a native round-trip check after the dependency is fixed. Other hosts remain
unverified for this edge case.
Human text escapes terminal controls, including ANSI/OSC and bidirectional format
controls. Plain line input still relies on the terminal's native editing. Partial
characters not delivered by the terminal cannot be recovered. A partial final
line received at EOF is saved as literal text and never interpreted as a command.

`--json` writes JSONL only. App events retain their `version`, `type`, task ID,
attempt ID and sequence. CLI replies use `{version:1,type:NAME,data:VALUE}`.
Names include `help`, `version`, `models`, `accounts`, `tasks`, `task`, `route`,
`doctor`, `backup`, `migration`, `recovery`, `attempt`, `error`, and `workflow_plan`.
`tasks` (the alias for `tasks list`) returns an array of task summaries as `data`,
with additive top-level `pagination:{limit,search,cursor,next_cursor}`. Summaries
contain ID, title, revision, workspace, status, `has_draft` and update time, without
draft text or selection bodies. The default page size is 20; `--limit 1..100`
changes it. `--search TEXT` matches literal title or ID substrings across the whole
archive, with ASCII case folding and a 256-byte UTF-8 query limit;
`--cursor TOKEN` requests the next page. An empty search or cursor explicitly
clears it. Cursors are opaque and bound to the exact search text. Tasks are newest
first within a live list: new or edited tasks may move above an older cursor.
Omit the cursor, or use interactive `/recent`, to refresh; no snapshot is promised.
These search/cursor flags apply only to `tasks list`.

A task reply contains
`{task,attempts,pagination:{limit,before_revision,next_before_revision}}`.
`tasks show ID` defaults to the latest five attempts. `--limit 1..100` changes
the page size; `--before-revision R` selects attempts strictly older than R.
Zero selects the latest page, and a zero next cursor means there is no older page.
Attempts are chronological within each page. Human output includes the exact next
command when more history exists. `--before-revision` applies only to `tasks show`; task
metadata refreshes also use bounded reads instead of loading complete history.
If complete results exceed the store's aggregate page bound, history retries a
smaller page down to one attempt. JSON reports the effective `pagination.limit`,
human output reports the actual count, and the older cursor stays stable. No
output is truncated; unrelated storage errors are returned without retrying.
`attempt` is emitted when Run returns an actual durable attempt, including a
replay or settled failure. A later `error` describes an unsuccessful command;
the exit code is authoritative. No attempt is invented for a blocked draft.
Durable warnings remain visible without changing a confirmed completion into a
failure: live warning events and replayed warnings go to human stderr, while
`tasks show` and `/result` include saved attempt warnings alongside the result.
JSON carries warning events and `attempt.warnings`; terminal controls are escaped.

The renderer queue is bounded at 32 writes with 500 ms queue and flush waits. Stalled output returns a display failure that detaches observation without canceling accepted work. The foreground owner drains and waits for its worker, while explicit interruption can still request a stop. Durable `Follow` delivers contiguous saved events with cursor validation, and an observer failure does not determine the execution outcome. Arbitrary caller-owned `io.Writer` and `io.Reader` values cannot be forcibly interrupted, leaving at most one blocked renderer writer and one input reader per invocation until the caller releases them. `App` separately caps observer callback workers at 16, including stalled callbacks. The flush barrier acknowledges queued writes without closing the producer queue, so late callbacks fail safely rather than panic. Process shutdown requests stop and joins owned work before closing the store; if cleanup remains unconfirmed, UCLI reports that state without claiming remote cancellation or detached persistence.

## Verification

The core Go test suite and repository checks (`tools/dev.py` and `tools/check_app.py`) run against the real local `internal/app` runtime and store using synthetic inputs, performing no live native inference by default. From the repository root, run `go test ./internal/cli` and `go vet ./internal/cli`. See the [hidden Windows ConPTY route](conpty.md) and [shared verification route](../../docs/verification.md) for authorization boundaries and exact execution recipes. Unit tests and captured terminal frames do not prove human visual comfort, accessibility, or live provider execution, and cross-compiled binaries do not substitute for native host execution. Always report failed, skipped, or unreached checks explicitly.
