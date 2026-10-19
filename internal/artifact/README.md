# Lazy pinned artifact cache

This internal package is a file acquisition foundation. The base executable does
not bundle native CLIs or transcription weights. Creating a `Cache` and calling
`Find` perform no downloads. A caller deliberately calls `Ensure` at first use;
this package has no startup hook, background prefetch, installer or executable
activation. It does not read project configuration or discover remote manifests.

```go
cache, err := artifact.New(absoluteUCLICacheRoot)
// Handle err. The caller chooses UCLI's dedicated local cache, not a workspace.
file, err := cache.Ensure(ctx, reviewedManifest, artifact.Options{Offline: offline})
// Handle err before using file.Path. Verification is not runtime admission.
```

`Manifest` pins ID, version, HTTPS URL, lowercase SHA-256, positive `MaxBytes`
(up to 8 GiB), kind, and OS/architecture. Kinds are `Runtime` and
`TranscriptionModel`; only model data may explicitly use `Platform{"any","any"}`.
The caller supplies shipped or reviewed catalog entries. A hash establishes byte
identity, not publisher trust, permission safety or account/runtime compatibility.
There is no public insecure transport option or test-only acquisition toggle.

`Find(ctx, manifest)` fully hashes an existing blob and returns typed `Missing`
when absent. `Ensure(ctx, manifest, Options{Offline:true})` permits the same verified
reuse, returning typed `Offline` when absent. Other typed `*Error` codes distinguish
invalid requests, unsupported platforms, failed acquisition, integrity failures,
unsafe cache paths and local IO. Messages omit remote URLs, signed query strings,
response bodies and raw native errors. `errors.Is` retains context cancellation
and deadline identity. `File` returns path, hash, actual bytes, reuse status and
directory-sync warnings.

Only explicit acquisition creates directories or sends HTTPS requests. Downloads
use the system trust store, at most five HTTPS redirects (including cross-host
CDN/signed storage destinations), no URL userinfo, a ten-minute total deadline,
and streamed `MaxBytes+1` enforcement. Non-200, compressed, truncated, oversized
and hash-mismatched responses never publish a file. Local TLS servers and private
client injection are used only in the package's tests; no actual provider files
are downloaded by verification.

Blobs live at `ROOT/artifacts-v1/sha256/HASH/blob`. Only hash-derived path components
are used. Newly created directories and staging files receive owner-only modes
or Windows ACLs; preexisting paths are never chmodded. An explicit cache-root
ancestor is resolved to its physical path once, then descendant link/reparse
entries are refused. Existing damaged, foreign or nonregular destinations are
left untouched and reported. The calling application must choose and protect its
own dedicated cache root; this is not a sandbox against hostile same-user edits.

The private temporary file is hashed, synced and closed before an atomic,
non-overwriting hardlink publishes it. Cooperating concurrent requests can download
the same blob twice, but only one publishes: other callers fully verify the winner.
There is no stale lock to expire or guessed ownership to steal. Filesystems without
hardlink publication fail explicitly. Canceled/failed requests remove their own
partial file; process death can leave an inert `.download-*` file, which is never
used as a cache hit. No automatic scavenger deletes unfamiliar files. If cancellation
arrives after publication, a complete verified blob can remain; retry verifies it.
Directory sync failures are returned as warnings, not a claim of physical
power-loss durability. The consumer must preserve those warnings.

Deliberate ceilings: no archive extraction, package manager, shell, executable
permission, launch, signed remote manifest/update system, multi-file transaction,
quota policy, resumable download or garbage collection. A model needing several
files must acquire and verify each pinned blob before a separate consumer
materializes or activates the bundle. No successful acquisition admits a native
CLI, account, model inference, transcription engine or permission policy.

## Verification

Use the project's pinned Go and external module/build caches:

```console
go test -p=2 ./internal/artifact -count=1 -timeout=40s -v
go vet -p=2 ./internal/artifact
```

The consuming tests use real local TLS requests and filesystem publication: lazy
and offline behavior; verified reuse without network; hash, length, truncation,
status, encoding and redirect negatives; system TLS rejection; cancellation;
concurrent independent caches; and foreign winner/path preservation. Link-negative
tests explicitly skip when the host cannot create a symlink. Cross-compilation
does not establish macOS/Linux filesystem, ACL, TLS or power-loss behavior.
