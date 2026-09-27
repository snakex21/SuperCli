# Bounded default for read_context with an omitted center

## Evidence

The recorded Muse continuation (muse-native-first-2) calls:

    read_context {"file":"service/publish.go","radius":20}

The tool schema accepts an omitted line, but the implementation initialized its Go integer to zero. The strict file library therefore returned "line=0 must be >= 1". The model's next call was read_lines for the same file.

An exact replay uses the saved synthetic workspace and those recorded arguments. With the fix, the first call returns the same 430-byte numbered source/EOF result as the recorded read_lines retry.

## Change

Initialize only the omitted center to line 1 before decoding arguments. Explicit centers still override the default and undergo the same strict library validation, including rejecting zero, negative numbers and positions past EOF. The line argument's description now states its default and is shorter.

The read remains bounded by the existing radius: default radius 10 gives at most 11 lines from the beginning; an oversized radius is still clamped to 250. Explicit centers, path sandboxing, binary detection, line-length limits, EOF reporting and cancellation retain their existing behavior.

No new prompt block, provider branch, background work, file scan or model call was added. The common tool implementation serves both native and dispatcher paths. The special Zen transport is untouched.

## Verification and limits

Before-fix regressions reproduce the failure for the recorded radius, missing radius, oversized radius, short-file fallback equivalence, cancellation and both invocation routes. They pass after the fix. Explicit invalid centers still fail; valid explicit centers preserve their requested window. Existing file-library context tests pass unchanged.

The exact saved-request replay produces byte-identical content to the saved retry, with one read_context execution. This removes the reason for that particular recovery call; no live reduction in total model requests or latency is claimed. A model may still independently choose another read.

Full go test -timeout=90s ./..., go vet ./... and CLI/GUI builds pass. CLI --help passes and both portable executables were installed after verification.

Artifacts: .tmp/read-context-default-2026-09-27/ contains the before source, red/green tests, replay source/result, full checks, builds and installation records. The frozen source trace is .tmp/worker-continuation-protocol-2026-09-27/muse-native-first-2/result.json.
