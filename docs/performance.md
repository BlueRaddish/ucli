# UCLI performance and responsiveness plan

Status: proposed acceptance budgets, 2026-10-17; unchanged during the October 18
implementation wave. The numbers below are targets. The
[Phase 0 receipt](verification.md) records measured
fixture overhead separately; it does not certify production or provider latency.
Performance is a product requirement because extra waiting or unreliable input
will send users back to native clients.

The [application evidence](verification.md)
records an actual 10,000-task/180 MB startup regression and its correction route.
The proposed budgets below remain unchanged. Local startup now performs bounded
schema/header checks; the full O(database bytes) SQLite audit is an explicit
`doctor` operation. Ordinary startup is not whole-database certification.

## 1. Measure the overhead UCLI controls

Separate local startup, configuration resolution, credential access, adapter startup, provider handshake, model queueing, first visible content, and stream rendering. Record spans using a monotonic clock and a shared run ID. A fast language cannot remove a slow quota probe or serial network call added to every prompt.

Report both absolute user-perceived latency and **incremental UCLI overhead** against the same provider integration. Compare matching model, account, effort, workspace, prompt, history size, permissions, and output mode. A native terminal UI and a structured runtime may have different setup costs; label that comparison instead of attributing the entire difference to UCLI.

A reference machine is a recorded developer-class laptop with SSD, at least 4 CPU cores and 16 GB RAM, normal OS security software enabled, and no competing heavy workload. Run Windows x64, macOS ARM64, and Linux x64; add other supported architectures to release validation. Record exact hardware, power mode, OS, filesystem, runtime, provider version, terminal, and security-software state. Do not disable antivirus to manufacture acceptable results.

“Warm” means dependencies/configuration are cached and the intended provider runtime is ready. “Cold UCLI” means a new UCLI process with no supervisor. “Cold provider” means a new native runtime. First installation, update scanning, interactive sign-in, and expired-token recovery are separate scenarios, never quietly pooled into warm measurements.

## 2. Proposed budgets

These are initial product targets on the reference matrix. Validate and revise them with evidence before making public guarantees. Any accepted revision needs a recorded reason; provider delay is not a reason to hide local overhead.

| Operation | Initial p95 budget | Conditions |
|---|---:|---|
| `--help`, version, shell completion | 100 ms end-to-end | Packaged executable; no network, adapter launch, or writable-store dependency |
| Cached models/accounts/tasks list | 150 ms end-to-end | 20 connections, 500 models, 10,000 task records; bounded first page |
| Cached interactive home ready for input | 250 ms end-to-end | Cold UCLI with cached local state; prompt and visible actions usable without network discovery |
| Resolve explicit route | 25 ms local CPU/wall span | Cached capabilities and policy; excludes OS secret-store interaction |
| Start foreground run | 150 ms added over matching direct launch | Cold UCLI, healthy local credentials; no provider discovery network call |
| Start through warm supervisor | 75 ms added over matching direct launch | Local authenticated IPC; same resolved request |
| Cold provider/auth handshake | 150 ms local added overhead | Provider handshake duration reported separately; no invented provider SLO |
| First useful local feedback | 100 ms | Show starting/connecting state when work continues; never fake token activity |
| Stream event to visible update | 25 ms added | Deterministic paced stream; bounded renderer batching |
| Cancellation acknowledgment | 100 ms | Local acknowledgment; provider stop tracked separately |
| Cooperative child shutdown | 2 seconds target | Fixture supports interruption; escalation has its own explicit deadline |
| Idle supervisor | Under 75 MB resident memory; under 0.5% of one core averaged over 60 seconds | No active runs; report platform measurement method |
| Streaming memory growth | Bounded independently of transcript length | Test 100 MB stream; persisted transcript and bounded viewport |

An OS credential prompt requiring human interaction is outside the startup SLO, but must appear promptly with clear ownership. A valid silent token refresh may incur provider latency; show and measure that phase separately. Provider busy/queue responses, network outages, and quota waits do not constitute local budget failures when UCLI remains responsive and correctly attributes the delay. They still count toward the user-visible completion distribution and reliability report.

The service must never trade away a required authentication or authorization check to meet a timer. Cache stable identity/capability facts with provenance and expiry; invalidate them after credential rotation, provider update, or relevant errors.

## 3. Pre-implementation benchmark gate

Build minimal disposable spikes before committing the application framework. Each must package a runnable artifact and perform the same operations: parse a command, read configuration, query a small SQLite fixture, start a child, stream framed output, accept cancellation, and clean up descendants. Include Windows wrappers and terminal paths immediately.

Compare Go with the strongest practical alternative after a small packaging feasibility check of Rust, TypeScript/Node, and Python. Do not build four miniature products. If Go comfortably meets budgets and has manageable integration dependencies, stop the comparison. Reopen it only when a concrete bottleneck or packaging constraint warrants another candidate.

For deterministic workloads, run at least 30 cold-start trials and 100 warm trials per matrix cell, randomized in candidate order. Report p50, p95, maximum, errors, and variability; retain raw timings. Use release builds and shipped packaging. Distinguish a fresh process from a cleared OS filesystem cache. A second independent run checks whether the result is stable.

Use a local fixture child to measure overhead without spending model quota. It should emit paced tokens, flood stderr, split frames, stall, ask for input, spawn a grandchild, ignore interruption, and exit with failures. Validate the harness using a known delay and a deliberately broken cleanup case. Then run a small authorized real-provider comparison to establish that the fixture did not omit handshake, credentials, or terminal behavior. Stubs prove local mechanics only.

Acceptance requires all planned reference OS routes, not a Windows-only claim of portability. If a target host is unavailable, record that gate as unexecuted and keep the language/platform decision provisional.

## 4. Keep the normal path short

Resolve explicit settings locally. Load only the selected adapter. Read cached catalog/usage information without launching all providers. Refresh stale facts on relevant events and with bounded timeouts. Avoid model-based classification for trivial or explicitly routed requests; uncertain routing can use an independently budgeted classifier with a deterministic fallback.

Treat account identity and quota observation differently. Identity necessary to prevent using the wrong account must be established; a missing quota percentage must not force an unnecessary login or fabricate precision. Preserve provider runtime sessions only when their documented lifecycle supports reuse. Never pool across account or workspace-policy boundaries.

Persist semantic transitions immediately; batch token deltas. Start with bounded renderer coalescing of at most 16 ms and measure its visible effect. Keep DB queries indexed and paginated. Use a small fixed worker limit before adaptive concurrency. Cache route classifications by relevant task/policy inputs and invalidate on account/capability changes.

Do not poll an LLM continuously. Route at dispatch, meaningful failure, or explicit checkpoint. A quota reset triggers local reevaluation; it does not automatically justify another model call. Timer-driven refreshes use jitter and backoff, suspend awareness, and an idle ceiling.

## 5. Reliability and regression workloads

Run one, four, and eight simultaneous fixture sessions, including two connections to the same provider and shared quota buckets. Measure head-of-line blocking, pipe progress, fairness, cancellation, database contention, and memory. A blocked approval in one run must not freeze another stream.

Test a 100,000-event reconnect, a long transcript, a slow terminal, a closed stdout pipe, a full disk, a provider crash during output, and a machine suspend/resume. Keep critical state correct before optimizing throughput. SQLite readers must release snapshots so checkpoints do not grow the WAL indefinitely. [SQLite WAL checkpoint guidance](https://www.sqlite.org/wal.html)

Every receipt records build hash, command, fixture revision, environment, raw samples, failed/skipped counts, and logs after redaction. Store baseline receipts outside temporary workspaces. CI runs deterministic smoke budgets; scheduled/release jobs run platform and real-provider checks. Flag a reproducible p95 regression above 20% even if still under budget, then investigate its absolute impact before blocking a release.

The release report must show both the overhead budget and task-level evidence: time to meaningful progress, successful cancellation, resumable context, and whether users complete routine tasks without looking up provider syntax. Speed is useful only when the common interaction remains dependable.
