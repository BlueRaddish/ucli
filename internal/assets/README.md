# Explicit model preparation

`Catalog()` returns a fresh copy of reviewed model metadata. It performs no IO.
`PrepareModel(ctx, absoluteCacheRoot, modelID, offline)` is called only for an
explicit voice/model request. It neither launches a runtime nor performs inference.
An unknown model is refused before filesystem or network access.

The first entry is `whisper/tiny.en`, consumed by a separately admitted
`faster-whisper` runtime. Its four files total **78,090,594 bytes** and are pinned
to official revision `0d3d19a32d3338f10357c0889762bd8d64bbdeba`. Their source is the
[official model metadata](https://huggingface.co/api/models/Systran/faster-whisper-tiny.en?blobs=true)
and the exact revision URLs stored in `catalog.go`. The coordinator retained the
metadata/small-descriptor audit in `.build/claude-ui-voice-research/receipt.json`.
Model weights were not fetched during development or verification. This package
does not list or acquire any native runtime artifact.

```go
model, err := assets.PrepareModel(ctx, cacheRoot, "whisper/tiny.en", offline)
// Handle err and preserve model.Warnings before passing model.Directory to the
// separately reviewed runtime's local-only model loader.
```

Preparation calls `artifact.Cache.Ensure` for each portable `any/any` data blob.
The directory contains copies, not links to writable artifact blobs. Each copy
must match its exact size and SHA-256. Newly created directories/files receive
owner-only permissions or Windows ACLs. Nothing changes permissions on preexisting
ancestors. The caller supplies its dedicated local UCLI cache root; existing root
aliases are resolved once, and descendant links/reparse points are refused.

The bundle lives in a private random directory under `ROOT/models-v1`. After all
files and its ownership receipt are synced and closed, a hardlink publishes that
receipt at `ROOT/models-v1/CATALOG_SHA256.json`. This manifest is the atomic
completion point; it references only the exact guarded sibling directory. No
portable overwrite-prone directory rename is used. `ManifestSHA256` identifies
the reviewed catalog entry, including its filenames, URLs, sizes and hashes.

Reuse checks the receipt's exact schema/catalog identity, matching private receipt,
expected directory entries, and every file's full size/hash. It makes no acquisition
call. Offline preparation may copy already verified artifact blobs into a complete
bundle, but never fetches missing bytes. A matching concurrent publication is
verified and reused; a foreign, damaged, linked or unexpected entry fails without
overwriting, repairing, deleting or chmodding it.

Cancellation and ordinary failures remove only the operation's own known staging
files and directory. Cleanup failures are returned. A completed published directory
is never removed, including if cancellation arrives after publication. Process
death can leave an inert private directory; there is no automatic scavenger.
Directory-sync limitations are returned as warnings. This is not a power-loss
durability guarantee or protection against hostile same-user filesystem mutation.
Filesystems without hardlink publication fail explicitly. Copying a model uses
additional disk space roughly equal to its blob size; disk failures retain the
same incomplete/publication boundary.

## Verification

Use the project's pinned Go toolchain and external caches:

```console
go test -p=2 -count=1 -timeout=45s -v ./internal/assets
go vet -p=2 ./internal/assets
```

The consuming materialization tests inject a private ensure function returning
synthetic local files. They exercise complete publication, full offline reuse,
portable artifact requests, exact hashes/sizes, wrong/foreign manifests and files,
traversal refusal, cancellation, competing preparations and cleanup. The public
`PrepareModel` offline-missing test calls the real artifact API and creates no
cache. The acquisition package separately tests real local TLS; this package's
fixtures make no HTTP requests. A symlink negative explicitly skips when host
privilege is unavailable. Cross-compilation does not establish native macOS/Linux
filesystem/ACL behavior, actual model acquisition or successful transcription.
