# Hidden Windows terminal verification

`TestPackagedConPTY` is opt-in and runs a previously built Windows executable
through the Windows pseudoconsole API. It creates no desktop terminal, does not
send desktop keyboard input, and does not read or change native credentials.
The packaged app uses an isolated UCLI home, temporary workspace and local demo.

After building `./cmd/ucli`, set `UCLI_CONPTY_BINARY` to that executable's absolute
path and `UCLI_CONPTY_EVIDENCE` to an evidence directory, then run:

```console
go test -p=2 -count=1 -timeout=45s -run TestPackagedConPTY -v ./internal/cli
```

The test logs the executable SHA-256, writes raw terminal streams and JSON
receipts, checks persisted state, and cleans up only its owned child/job/console.
Without `UCLI_CONPTY_BINARY` the test explicitly skips; a skip is not a pass.
The redirected unit tests remain in the ordinary suite.

The harness starts its output drain before the child, creates the child suspended,
assigns a kill-on-close job, and then resumes it. Reads and writes have separate
workers and bounded waits. Output keeps draining during console teardown, as
required by [Microsoft's pseudoconsole lifecycle documentation](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session).

For the attached pseudoconsole child, `CREATE_NO_WINDOW` is deliberately omitted:
that flag suppresses console handles. The pseudoconsole attribute plus `SW_HIDE`
provides the hidden terminal, and a bounded incompatible-flag control records
its unsuccessful prompt route and confirms owned termination. This is distinct
from ordinary piped subprocesses, which retain `CREATE_NO_WINDOW`.
[Microsoft's process flag reference](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)
explains the handle behavior.

`STARTF_USESTDHANDLES` with null standard handles prevents a redirected harness's
streams being duplicated into the child instead of console handles. This is
the [Microsoft maintainer's documented redirected-host case](https://github.com/microsoft/terminal/discussions/15814).
The first attempts preserved in the coordinator's receipt failed before this
correction; no successful terminal behavior was inferred from those attempts.

The actual console route found a product bug: Go's ordinary `os.File` console
reader mapped both a zero-character interrupted read and Ctrl+Z to EOF. UCLI now
reads genuine Windows console input through the existing `ReadConsoleW` binding,
retains native cooked-line editing and UTF-16 conversion, and distinguishes
interruption (130) from the actual Ctrl+Z EOF character (0). It changes neither
console modes nor signal handlers, and adds no timing guess. See
[ReadConsole](https://learn.microsoft.com/en-us/windows/console/readconsole) and
[console control signals](https://learn.microsoft.com/en-us/windows/console/ctrl-c-and-ctrl-break-signals).

Observed on Windows x64, October 18, 2026: the interactive prompt, searchable
model selection, explicit Send, task lookup/result, New, resize from 80x24 to
60x20, Korean and non-BMP input, restart restoring an unsent draft, idle ETX exit
130, and Ctrl+Z/Enter EOF exit 0 all passed. The saved draft remained unsubmitted
through both restart/exit paths. The controlled `CREATE_NO_WINDOW` route recorded
WAIT_TIMEOUT and was terminated within its owned job; no natural exit is claimed.
The original ETX failure returned 0 and is retained alongside the corrected run.

This is real Windows console-path evidence with a simulated executor. It is not
owner usability, screen-reader validation, live provider interruption, terminal
emulator visual review, or evidence from Linux/macOS hosts. The harness strips
terminal control sequences only for text assertions; raw streams remain intact.
