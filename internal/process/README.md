# Experimental owned process transport

`Start` requires absolute executable/directory paths and a nonnil explicit
environment. Empty `Env` means empty, never ambient inheritance. The lifetime
context controls the child; `Wait(ctx)` only bounds waiting and returns a cached
result. Native nonzero exit codes are values, while transport, cancellation and
cleanup failures are errors. `Close` stops the owned tree and waits at most five
seconds; an error is not proof of successful cleanup.

`Send` leaves stdin open, serializes bounded payloads, and has a two-second ceiling.
A deadline while writing stops the process because partial protocol input is unsafe
to replay. A caller deadline while awaiting the write gate does not stop other work.
`FrameBytes` limits each Send payload and raw output chunk; it does not parse JSONL.

Readers never call downstream writers. They deliver raw stdout/stderr chunks to a
bounded channel. Output chunks use at most 4 KiB (or a smaller configured frame
limit); channel capacity conservatively counts full chunks against `QueueBytes`.
A full channel fails with `ErrBackpressure` and stops the tree. Consumers must check
`Wait` even after output EOF. Pending buffered chunks remain readable after exit.
Memory includes the bounded output queue, two reader chunks and one bounded stdin
write, plus ordinary channel/runtime bookkeeping.

Windows starts hidden and suspended with explicit environment, cwd and inherited
pipe handles, assigns a kill-on-close job, then resumes the returned primary thread.
Linux uses a process group. Its exit observer uses `waitid(WNOWAIT)` so the leader
remains unreaped while the owned group is signaled. A shared guard permits one
group stop, then forbids signals before `cmd.Wait` releases the leader identity.
This fixes the previous reap-then-group-kill ordering, which could target a reused
numeric PGID after a fast, childless exit. The guard never holds its mutex across
a potentially blocking reap. `ECHILD` is lost ownership: no further numeric signal
or wait is attempted, and cleanup uncertainty remains an error. The Linux owner
must be the sole reaper of its child; unrelated wildcard waits or changing
SIGCHLD auto-reap behavior would violate that ownership contract.

**macOS owned process launch is explicitly unsupported.** `Start` returns
`ErrUnsupportedPlatform` before executing any child. The CLI, state and local
in-process demo can still build and run; external agent execution is not claimed.
Darwin `NOTE_EXIT` alone has an exit-before-registration race. Apple emits that
notification before publishing zombie state, so a single follow-up zombie query
is also insufficient. A possible timed observer needs host validation; the
available zombie-query sysctl is an Apple SPI. No guessed fallback is enabled.

`Alive` remains conservative PID liveness on all three platforms for demo recovery,
never evidence of ownership; a PID reuse false-positive must block recovery.

Run the real child/helper checks using the repository Go toolchain and shared
verification route:

```console
go test -count=1 -timeout=45s -v ./internal/process ./internal/workspace
go vet ./internal/process ./internal/workspace
```

Windows tests cover explicit environment/cwd/Unicode/argument quoting, stdin reuse,
unread output, stalled stdin, wait-versus-lifetime cancellation, owned descendants,
invalid input and observable injected cleanup failures. Workspace tests create a
real Git repository/worktree and a junction/symlink. Helpers have bounded lifetimes
and never call a provider. Shared guard tests exercise signal/reap ordering,
concurrent stop calls during a stalled reap, stop-once failure retention and lost
ownership. Darwin child-dependent fixtures explicitly skip; its dedicated negative
test checks that the unsupported path never creates a child marker. Pure guard
tests remain available there. Actual Linux/macOS execution, PTY/ConPTY, wrapper-shell
semantics, abrupt POSIX owner death and native termination-error fault injection
remain admission work. This package does not turn process groups into a sandbox or
protect against a deliberately escaping child. No performance claim is made here.

The Linux lifecycle correction was cross-compiled and its shared ordering guard
executed on Windows. WSL listed Ubuntu but actual execution failed with
`Wsl/Service/E_UNEXPECTED`; no restart was attempted. Receipts under `evidence/`
keep that unreachable host result separate from compile and Windows test results.

Source checks for this decision: [POSIX process/PGID lifetime and reuse](https://pubs.opengroup.org/onlinepubs/009696699/basedefs/xbd_chap04.html),
[Linux waitid/WNOWAIT](https://man7.org/linux/man-pages/man2/waitpid.2.html),
[Apple kqueue](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/kevent.2.html),
[XNU event attachment](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_event.c),
[XNU exit ordering](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_exit.c),
[XNU sysctl implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sysctl.c).
Pinned local Go 1.27.2 `os/wait_waitid.go` and `os/wait_unimp.go`, plus x/sys v0.48.0
generated Linux Waitid and Darwin Kevent/SysctlKinfoProc bindings, were also read.
