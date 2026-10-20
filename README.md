# UCLI

<p align="center">
  <img src="assets/logo/ucli-loaf-outlined.png" alt="UCLI Loaf cat logo" width="240" />
</p>

[UCLI](https://github.com/BlueRaddish/ucli) is an experimental universal command-line interface for working with AI agents. Its purpose is day-to-day comfort: one discoverable place to choose models, switch accounts, set effort levels, and return to saved work without memorizing each vendor's command syntax and session flags.

This repository is a clean public source checkout for people deciding whether to try the local interface or contribute to its development. No public binary release is promised, and no private development history or host verification evidence accompanies this checkout.

## Quick Start and Building

UCLI requires Go `1.27.2` and uses the Python `3.13` standard library for build and verification scripts. You can explore the interactive interface immediately in demo mode, which creates isolated local state and simulated agent responses without using any AI quota:

```sh
go run ./cmd/ucli --home .build/demo-home --demo
```

Type a prompt and press `Enter` to receive a simulated local reply. Press `Tab` to discover actions, run `/models` to browse model choices, or run `/sessions` to inspect saved activity. The demo uses no provider quota.

To run the repository checks and package binaries from a single command, invoke the developer runner with your Go executable path:

```sh
python tools/dev.py all --go PATH_TO_GO --jobs 1
```

Packaged outputs are written to `.build/bin/ucli-windows-amd64.exe` (Windows), `.build/bin/ucli-linux-amd64` (Linux), and `.build/bin/ucli-darwin-arm64` (macOS). These cross-builds verify that the source compiles cleanly for each target, but they are not native host proof.

## Terminal Interface

The fixed rich header shows the static cat mascot, version, and task context, while the framed bottom editor places notifications above the input box and the active model and working path below it. `PageUp` and `PageDown` scroll the output body. `Enter` submits a nonempty prompt, `Alt+Enter`, `Shift+Enter`, or `Ctrl+J` adds a newline, and an empty `Enter` never starts work. Restored unfinished text is editable, and paste or voice input inserts reviewable literal text without auto-submitting. `Tab` discovers actions and completes slash commands, and `/send` provides an explicit submit equivalent. Display flags preserve layout and input: `--no-color` and `NO_COLOR` strip color only, `--no-motion` keeps cats static, `--ascii` switches only cats to ASCII, and `--plain` uses the static line interface. Additional interface details are in [internal/cli/README.md](internal/cli/README.md).

## Local Storage, Workflows, and Voice

Local state is persisted in SQLite, covering saved tasks, drafts, and results. Each task tracks its requested model, account, and effort settings independently, supported by replay protection, search and paging, explicit backups, and backed-up schema migrations. Core design boundaries are described in [docs/architecture.md](docs/architecture.md).

You can validate and inspect workflow definitions locally using `workflow check` and `workflow view examples/review.workflow.json`. These commands only validate and display the graph; they never execute a workflow. Optional local voice input starts only on explicit request and inserts reviewable text into the composer rather than sending automatically. Only synthetic voice fixtures are tested today; real microphone hardware, ASR quality, and backend downloads remain unverified.

## Native Sign-In and Execution Boundaries

Opening `/models` reports whether each provider binary is found, its cached sign-in state or unchecked status, and whether execution is supported as experimental or unavailable. Blocked models can be inspected without changing your selection. Ordinary listings avoid native processes and network calls; explicit setup or refresh reads native metadata during browsing. Claude aliases are inspectable, and the reviewed adapter targets version 2.1.299, so other installed binaries require review even when signed in natively. AGY can refresh and cache its native catalog, though UCLI sign-in verification and execution remain unavailable. GitHub reads local gh identity only, offering no AI models or hosted UCLI account, and credentials stay in native stores. Use `ucli login codex`, `ucli login claude`, or `ucli login github` for explicit native connection commands; see [docs/native-signin.md](docs/native-signin.md).

Native execution envelopes for Codex `0.162.1` and Claude `2.1.299` on Windows x64 remain experimental, and there is no confirmed successful end-to-end native AI response yet. The read-only default disables native tools. Passing explicit `--permission sandbox` grants full local computer access through the native client with no extra OS isolation. Tasks that end with unknown outcomes keep a safety guard; native reconciliation is not implemented, so do not retry uncertain runs blindly. Follow-up prompts send only new input into fresh native sessions rather than replaying prior conversation automatically. Currently, `auto` selects the default supported model at the lowest supported effort. Account cycling, provider quota reservations, workflow execution, and a background model/effort decision agent remain future work.

## Contributing and License

Contributions are welcome, especially on small issues, offline protocol fixtures, task recovery, cross-platform portability, and terminal usability. See [CONTRIBUTING.md](CONTRIBUTING.md), [docs/verification.md](docs/verification.md), and [docs/next-development.md](docs/next-development.md) before opening a pull request.

Released under the MIT License ([LICENSE](LICENSE)), copyright 2026 BlueRaddish.
