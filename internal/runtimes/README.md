# Lazy native runtime bytes

`Catalog` is shipped metadata only. `Prepare` runs only for an explicit runtime
setup/first-use request. It prefers an existing exact reviewed executable, then
uses the verified artifact cache. Unrelated native installations remain untouched.
There is no download, native version probe, login or update during ordinary help,
startup, model listing, task browsing or workflow inspection.

The initial downloadable catalog is Claude Code2.1.299, with six platform byte
identities from its official signed release manifest. The GPG signature was checked
in an isolated keyring against fingerprint
`31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE`. Official manifest and URL HEAD evidence
are retained separately; no runtime binary was downloaded for these checks.
Windows matches the locally audited executable. Linux entries require glibc;
additional operating systems/architectures remain extensible catalog entries.

This package acquires bytes. It never executes them, imports native credentials,
changes PATH, runs a remote shell installer or changes executable permissions.
Preparing a runtime does not pass managed AI/account/permission/platform admission.
Future adapters must call this seam at first requested use and separately verify
the actual launch envelope; mutable external paths need rechecking before launch.

Checks: `go test ./internal/runtimes`. Actual local TLS download/cancellation/hash/
publication failure routes belong to `internal/artifact`; these checks consume the
composition's existing-byte preference, manifest binding, offline forwarding,
foreign preservation and cancellation. No real native runtime is launched.

Primary source: https://code.claude.com/docs/en/setup#binary-integrity-and-code-signing
