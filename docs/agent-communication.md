# Agent communication: current behavior and next boundaries

This technical note separates implemented UCLI behavior from future
communication work. The reference is Reason
Network commit `039b9571dab11f980813918b60a827b8bb27fbfd`. Its source was inspected;
its program, tests, installer, network join and messaging were not executed.
This is a bounded architecture review, not a security certification.

## Current UCLI behavior

UCLI already has durable task, command and attempt IDs, command-intent replay,
owner checks and workspace leases. [Acceptance](../internal/state/attempts.go)
commits the attempt and initial event together. [Events](../internal/state/events.go)
have per-attempt sequences and bounded reads after a caller's cursor. A replay
does not create another inference request. Unknown outcomes retain their fences.

[Execution](../internal/app/execution.go) persists output before delivering it,
but still runs under the foreground caller's context. Its output callback can
fail execution; it is not a detached subscription. The process layer bounds raw
output queues and stops its owned child on overflow. Native Claude and Codex
submissions currently create fresh ephemeral contexts: saved UCLI history is
not provider conversation memory. There is no implemented local broker,
cross-machine agent bus or automatic native-session resume.

## Narrow adoption: monitor catch-up

The `/sessions` monitor drains bounded event
pages promptly while backlog remains and retains its cursor on a refresh of the
same task and attempt. Once caught up it resumes the ordinary refresh interval.
It keeps identity and contiguous-sequence checks, bounded output tails,
cancellation and generation guards; a different attempt resets the cursor.
No provider call, second store, state-format change or daemon is needed.

The transferable ideas are to wait only after checking available data and to
retain incremental progress. Reason's [inbox loop][inbox] registers interest
before checking messages, avoiding a lost notification between check and wait.
Its [transcript cache][offsets] reads appended complete records, retaining an
offset and resetting on truncation. UCLI uses its own durable event sequence;
this adoption does not read native transcripts or copy the Rust implementation.

Checks must prove multi-page catch-up without a normal polling delay per page,
no duplicate output on refresh, reset on a changed attempt, rejection of gaps
or foreign identities, bounded memory and responsive cancellation. Source review
alone is not a pass; release results belong in [verification](verification.md).

## Future local broker, only when needed

Reason separates thin clients from an owning daemon, using [Unix sockets or
Windows named pipes][ipc]. Its client [reconnects and re-registers sessions][reconnect]
with backoff. A future UCLI broker may start on the first explicit communication
or background-work request, without launching every native CLI at startup.
Keep implementation in Go and reuse existing state; no mandatory Rust or Reason
Network dependency follows from this reference.

The broker would own a turn independently of UI attachment. Subscribers reconnect
with attempt ID and last displayed sequence, reading durable output before waiting
for a notification. Bound frame bytes, outstanding requests, subscriber buffers
and catch-up work; a slow UI detaches or resynchronizes rather than cancelling the
worker. Register interest before reading to avoid lost wakeups. An explicit cancel
targets the owned attempt and still needs native terminal evidence. A timeout or
local process exit cannot prove remote cancellation.

Conversation lifetime is distinct from process lifetime. Persist native resume
handles only where the adapter verifies them; lazy process launch or idle closure
must preserve the provider/account/workspace/permission binding and one active
turn per context. This is future work under the [shared contract](contracts.md),
not a new background-execution claim.

## Addressing, delivery and authority

Reason models [push, next-turn, pull and unavailable delivery][tiers], separately
from the session's stable native key. UCLI should similarly expose only verified
adapter capabilities. Friendly names are labels, not authority; use immutable
IDs and refuse ambiguous targets. Do not infer a conversation from a PID or folder.
Reconnection restores subscriptions, never automatically resubmits inference.

Persisted, delivered, read, provider-accepted and completed are different facts.
Reason describes [at-least-once delivery][mail], with unacknowledged messages
eligible again after 90 seconds. Its [push receipt][push] can precede asynchronous
native delivery confirmation. Neither receipt proves an AI task completed.
Future message acknowledgements bind message ID and intended sender/recipient
session generation; an inbox message need not have an accepted attempt. Receiving,
reading or acknowledging it does not accept or dispatch AI work. Task-stream
acknowledgements instead bind subscriber, attempt and sequence. Only the existing
terminal-result rules can settle an attempt.

Local transport needs same-user endpoint access controls, authenticated instance
binding, bounded inputs and operation-specific permissions. Preserve native
executable/account admission and explicit permission pins. Remote messages remain
untrusted task input, never permission grants. Reason interoperability would be a
separate opt-in adapter: no implicit installation, joining, credential sharing,
external messages or permission widening.

## Reference limits and attribution

Do not copy the reference's resource ceilings: [local server channels and request
spawning][local-limits] and [client queues/line reads][client-limits] are unbounded;
[unread messages][unread] are materialized as a whole list. A network frame cap
does not bound those paths. At this pinned reviewed commit, the local `Delivered`
handler ignores the supplied session key; UCLI needs identity-bound validation.

The [package][license] declares `MIT OR Apache-2.0`; its [NOTICE][notice] credits
tincan contributors and Reason Machines' modifications. The proposed Go changes
are independently implemented design adaptations, not vendored Rust or an upstream
endorsement. Substantial future code reuse must retain the applicable licence and
attribution notices. Reference scope is the pinned source, not later releases.

[inbox]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/daemon/mail.rs#L633-L665
[offsets]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/doing.rs#L99-L154
[ipc]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/ipc.rs#L7-L24
[reconnect]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/client.rs#L120-L139
[tiers]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/proto.rs#L8-L55
[mail]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/daemon/mail.rs#L1-L33
[push]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/daemon/mail.rs#L376-L396
[local-limits]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/daemon/mod.rs#L260-L334
[client-limits]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/client.rs#L142-L170
[unread]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/src/store.rs#L254-L259
[license]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/Cargo.toml#L1-L7
[notice]: https://github.com/reason-machines/reason-network/blob/039b9571dab11f980813918b60a827b8bb27fbfd/NOTICE
