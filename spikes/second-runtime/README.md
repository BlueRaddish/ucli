# Second-runtime experiment (P03)

This is a disposable protocol experiment, not an installed UCLI adapter.
Python 3.10+ and its standard library are sufficient.

```console
python -m unittest discover -s spikes/second-runtime -p test_probe.py -v
python spikes/second-runtime/probe.py --muse PATH_TO_NATIVE_MUSE_BINARY --receipt .build/muse-receipt.json
```

Pass the explicit installed Muse executable. The probe bypasses the auto-updating
shim, uses a temporary workspace/home/config with no auth file, requests the native
`echo` provider, disables shell and file-write tools, and keeps sessions memory-only.
It never calls a model, copies credentials, changes native settings or grants trust.
It verifies native initialization, deduplicated session creation, two sequential
turns and a turn interrupt in one native session. A completed interrupt race is
recorded honestly and does not establish stopped-turn behavior. Output receipts contain build/schema identity and
booleans, not native home paths or account identity.

The transport drains stderr independently, bounds frames/events/session bytes,
refuses approval requests, bounds both pipe writes and replies, and shuts down its
owned process job/group through the shared Python spike helper. Stalled
requests have unknown external outcome; killing a host is not proof remote work
stopped. The 16 MiB transcript ceiling is deliberate for the spike; production
needs persisted/spilled streams. Its filesystem session lease is conservative:
exceptional/uncertain outcomes retain the fence and require reconciliation, not
clock-based automatic takeover. Temporary echo fixtures may then be discarded
because their known backend has no external model effects. Windows job assignment
has a startup race; containment is checked after handshake, not claimed for hostile
startup code. Production process creation belongs to the Go platform layer.

Tests exercise actual child pipes for malformed JSON, oversized frames, split UTF-8,
stderr flood, failure, stalled input reads, and descendants retaining pipes;
session exclusion is tested across processes; uncertain-lease retention is checked
after exceptional outcomes.
The Antigravity result normalizer rejects empty successes and timeout partials.

## Admission result

Muse is the stronger second bidirectional candidate observed here. The installed
native echo backend passes this transport slice, with no authenticated inference.
Windows reported its sandbox unavailable. Neither this echo run nor the presence
of disable flags establishes enforcement against hostile tools. Real Meta account
binding, startup-source suppression, persistent resume, quota and other OS paths
remain unverified. G0 and production adapter admission remain open.

Antigravity 1.4.2 exposes structured print and model/effort selection, but this
experiment has not established an isolated connection binding or a per-run
read-only/trust guarantee. No model prompt was sent. Keep it discovery/native-only
until those prerequisites are proven; do not equate plan mode with enforcement.

References: [Muse](https://dev.meta.ai/docs/muse-code),
[Muse permissions](https://dev.meta.ai/docs/muse-code/permissions),
[Antigravity headless](https://antigravity.google/docs/cli/headless/),
[Antigravity permissions](https://antigravity.google/docs/permissions/).
Exact installed schema fingerprint is retained in the native receipt; the full
generated schema remains under ignored `.build/muse-schema` to avoid a large copy.
