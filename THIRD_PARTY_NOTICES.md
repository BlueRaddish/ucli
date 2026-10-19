# Third-party components

The MIT license in this repository covers UCLI's original code and documentation.
Dependencies retain their own licenses. Go module versions and checksums are
recorded in `go.mod` and `go.sum`; no dependency source is vendored here.

Native AI clients, Python packages and speech models are separate products. They
are not bundled in the base executable or this source checkout, and UCLI's license
does not relicense them. Optional preparation downloads from the pinned upstream
source only after an explicit request; upstream terms and authorization still
apply. Preparing files does not grant account access or model entitlement.

Claude Code and Murmur informed interaction and voice-flow research. Their
implementation bundles are not included in UCLI. The public tree excludes the
private installed-program reference probe and its source anchors. The protocol
adapters and voice helper are UCLI implementations with synthetic test fixtures.
