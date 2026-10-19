# Contributing to UCLI

UCLI is an experimental source project. We welcome narrow, focused branches and pull requests that address small issues, add offline protocol fixtures, strengthen task recovery, or improve cross-platform portability and terminal usability. Before changing shared APIs, SQLite storage schemas, or permission boundaries, open an issue or discussion first so the approach stays aligned with [docs/next-development.md](docs/next-development.md).

## Prerequisites and Verification

Building and checking UCLI requires Go `1.27.2` and Python `3.13` using the standard library runner:

```sh
python tools/dev.py all --go PATH_TO_GO --jobs 1
```

Run existing tests and relevant consuming checks before submitting a change, and use synthetic, disposable `--home` directories (such as `.build/dev-home`) whenever exercising the CLI. No paid accounts or live AI inference are needed for normal verification checks. Follow the procedures in [docs/verification.md](docs/verification.md) and document any skipped or failed checks honestly in your pull request, including host platforms or native provider paths you could not run.

## Security and Admission Discipline

Never commit auth tokens, device codes, real account identities, private prompts, native credential stores, or unreviewed local receipts. Always rely on synthetic accounts and redacted fixtures.

Preserve adapter version pins and unknown-outcome safety guards. Never weaken native admission checks, read-only defaults, or replay protections just to make a test pass.
