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

Startup and native admission checks still run synchronously. After a prompt is admitted and saved, the rich shell returns to editing and menus while an in-process worker owned by this UCLI invocation runs it. Pressing `Enter` on a task that is already working saves the next prompt without queuing, steering, or sending it. `/sessions` inspects saved activity, and `/stop` requests a stop only for active work on the selected task owned by this process. Pressing `Ctrl+C` twice within the two-second confirmation window exits and requests a stop for this invocation's workers, as does closing UCLI. There is no detached service or promise that work survives terminal or process closure. See the [terminal UI research](docs/terminal-ui-research.md) for design context rather than usability evidence.

## Local Storage, Workflows, and Voice

Local state is persisted in SQLite, covering saved tasks, drafts, and results. Each task tracks its requested model, account, and effort settings independently, supported by replay protection, search and paging, explicit backups, and backed-up schema migrations. Core design boundaries are described in [docs/architecture.md](docs/architecture.md).

You can validate and inspect workflow definitions locally using `workflow check` and `workflow view examples/review.workflow.json`. These commands only validate and display the graph; they never execute a workflow. Optional local voice input starts only on explicit request and inserts reviewable text into the composer rather than sending automatically. Only synthetic voice fixtures are tested today; real microphone hardware, ASR quality, and backend downloads remain unverified.

## Native Sign-In and Execution Boundaries

`/models` reports installed binaries, cached or unchecked sign-in state, and experimental or unavailable execution separately. Blocked entries can be inspected without changing the current selection. Ordinary listing is local, while explicit setup or refresh reads native metadata. Claude model rows come from discovered native metadata rather than a hardcoded alias list, and models require a compatible reviewed build and supported capabilities. AGY can refresh and cache its native catalog only; UCLI sign-in verification and execution remain unavailable. GitHub reads local `gh` identity and provides no AI models or hosted UCLI account. Credentials stay in native stores. See `ucli login codex`, `ucli login claude`, `ucli login github`, and [docs/native-signin.md](docs/native-signin.md) for details.

Prior bounded native checks on Windows x64 completed small read-only requests with Codex `0.163.0` and Claude `2.1.304`. That provides evidence for those reviewed builds and requests, not universal model, account, or OS admission, and not proof for this new worker integration. Native execution remains experimental. The `read-only` default disables native tools, while explicit `--permission sandbox` grants full local computer access through the native client without added OS isolation. Unknown outcomes remain guarded: native reconciliation is unavailable, and UCLI does not blindly retry. Follow-up prompts send only the new input into fresh native sessions without automatic provider conversation memory. `auto` currently selects the default supported model at its lowest supported effort. Account cycling, provider quota reservations, workflow execution, and a background model/effort decision agent remain future work.

## Contributing and License

Contributions are welcome, especially on small issues, offline protocol fixtures, task recovery, cross-platform portability, and terminal usability. See [CONTRIBUTING.md](CONTRIBUTING.md), [docs/verification.md](docs/verification.md), and [docs/next-development.md](docs/next-development.md) before opening a pull request.

Released under the MIT License ([LICENSE](LICENSE)), copyright 2026 BlueRaddish.
