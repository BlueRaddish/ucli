# ADR 0001: provisional Go core with language-neutral adapters

Date: 2026-10-17. Status: proposed; accepted only after the packaging/lifecycle gate in [performance.md](../performance.md). Scope: new UCLI project. This does not change cly's runtime or installation.

## Context

UCLI needs a quick command path, portable distribution, reliable process orchestration, streaming, local persistence, and an approachable codebase for parallel development. Most work is external I/O and state coordination. We have no application benchmark establishing that one language will produce a better UCLI experience.

The decision must account for dependency packaging and provider integrations as well as execution speed. A requirement to distribute a runtime is manageable; a native binary alone does not guarantee a fast or correct application.

## Alternatives

| Candidate | Useful fit | Costs and uncertainties |
|---|---|---|
| **Go** | Native executables, standard process/network/concurrency facilities, relatively small language surface; suitable for a local orchestration core | Garbage collector and dependency choices still require measurement; SQLite/keychain/terminal libraries may complicate packaging |
| **Rust** | Native executable, explicit ownership, strong tooling and broad target support; attractive for strict resource/process control | Async and platform integration introduce implementation complexity; actual team delivery/maintenance cost is unmeasured |
| **TypeScript / Node** | Strong provider-SDK access and shared types with future web surfaces; rapid protocol/UI development | Interpreter/runtime or single-executable packaging to maintain; dependency startup and terminal behavior must be measured |
| **Python** | Fast iteration, familiar orchestration ecosystem, straightforward support scripts | Shipping the interpreter and dependencies needs an explicit packaging strategy; startup/import cost must be measured |

Go's build tool emits executables and supports target configuration. Native C dependencies introduce an additional cross-compilation toolchain requirement, so “one binary everywhere” is a packaging goal to prove rather than assume. [Go build documentation](https://pkg.go.dev/cmd/go), [cgo cross-compilation](https://pkg.go.dev/cmd/cgo)

Rust documents support tiers with differing build and test guarantees. UCLI must execute its own tests on supported targets even when the compiler supports them. [Rust platform support](https://doc.rust-lang.org/rustc/platform-support.html)

Node supports single-executable applications, but its documentation labels the feature active development and records platform limitations. Re-evaluate the chosen release's support during the spike; do not assume an npm-oriented prototype proves standalone distribution. [Node single-executable documentation](https://nodejs.org/api/single-executable-applications.html)

Python offers an embeddable Windows distribution whose dependency management differs from a regular installation. It remains viable when packaging and measured startup meet the same user budgets. [Python on Windows](https://docs.python.org/3/using/windows.html)

These sources establish platform/build facilities, not comparative UCLI speed. Preference for Go's maintainability is an architectural judgment; it needs confirmation through a representative change and review, not a fabricated productivity score.

## Decision

Use Go as the provisional core language. Begin with one module and one executable, application services separated from presentation, and a small set of platform ports. Choose a stable supported toolchain at implementation kickoff, pin it in CI, and document upgrades.

Built-in adapters may be Go implementations. External adapters speak the versioned stdio protocol and can use TypeScript, Python, Rust, or another language when a provider SDK warrants it. No provider SDK type crosses into core domain contracts. This avoids rewriting the product to accommodate the next vendor's preferred language.

Prefer a driver and platform libraries that preserve straightforward distribution, but select them through tests of maintenance status, licenses, actual bundled SQLite version, process behavior, and packaging. Do not write a database engine, cryptographic store, or terminal emulator merely to avoid a dependency.

## Gate and reversal

Before implementing the full task engine, produce packaged spikes for Windows x64, macOS ARM64, and Linux x64. Demonstrate command startup, real credential-store access, SQLite transactions/recovery, structured streaming, native terminal resize, Unicode paths, and complete process-tree cancellation. Record unavailable hosts as blocked evidence, not passes.

Keep Go if it meets the proposed overhead budgets and dependencies remain supportable. If a failure is architectural, fix that first. If evidence identifies language/runtime packaging as the limiting factor, benchmark the strongest alternative under identical workloads and revise this ADR before substantial core implementation.

The reversal cost is deliberately limited: protocol schemas, CLI semantics, fixtures, data ownership, and acceptance journeys are language-neutral. Internal Go interfaces are not public extension APIs. A later isolated component may use another language when measured benefits justify the extra release artifact; a whole-core rewrite requires broader evidence.

## Consequences

Parallel work can proceed against explicit contracts and fixtures after the spike. Contributors need Go for core work, while provider specialists can choose supported external adapters. Native distribution remains independent of cly. The project accepts some platform-specific code and packaging work in exchange for a consistent, low-overhead local entry point. No performance claim is made until the packaged user path has measured receipts.

## P01 evidence update — 2026-10-18

The [disposable Go spike](../../spikes/platform/README.md) now packages command/config,
SQLite transactions, framed streaming, owned process cancellation and workspace
identity/lease probes. Go 1.27.2 was installed portably from the official release
with SHA-256 verification. A CGO-free module pins modernc.org/sqlite v1.60.1 and
checks the actual loaded engine, which reported SQLite 3.53.4. Windows x64, Linux
x64 and macOS ARM64 cross-compilation, four Go checks and vet passed.

The same execution service ran under foreground and persistent fixture hosts on
Windows and WSL Linux. Windows initially missed the 150/75 ms local overhead targets.
Stage profiling identified a system-wide thread scan, and retaining the documented
`CreateProcess` primary-thread handle removed it while preserving suspended start,
job assignment before execution and hidden children. Two corrected 30/100 paired
trials measured cold p95 69.41/91.34 ms and warm p95 13.68/11.26 ms. This supports a
local implementation correction, not a language-performance comparison.

Linux's first WSL run passed; its repeat failed the cold-tail budget (203.07 ms p95).
Both used a Windows-mounted executable. A native WSL filesystem diagnostic could
not launch, so storage or host conditions remain hypotheses, not explanations.
macOS execution, actual credential stores and terminals, real-provider parity and
other product SLOs remain unverified. Slow-consumer backpressure and termination
failure paths are explicit P09 ceilings; lease recovery/migration are P08 work.

The [execution record](../verification.md) retains
successful and failed trials, exact binary identities, negative controls, host
conditions and skipped scope. Alternative runtimes were inventoried rather than
packaged/benchmarked. **This ADR remains provisional and G0 is not passed.** Continue
the missing experiments before freezing the production stack; do not promote this
fixture's simplified schemas, process wrapper or lease table into the core.
