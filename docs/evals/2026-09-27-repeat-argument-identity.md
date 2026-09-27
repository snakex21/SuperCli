# Distinguish repaired tool arguments from repeated failures

Date: 2026-09-27

## Confirmed defect

The shared repeat detector normalized JSON objects into arrays of sorted key/value pairs. This erased the difference between an object and an array already containing those pairs. A malformed patch with an array where a change object belongs could therefore poison the failure counter for the subsequently corrected patch. Two schema failures caused the corrected third call to be blocked before execution.

A real temporary-file regression reproduces the failure through both native tool calling and the thin sentinel dispatcher. The file remains unchanged after the two rejected calls; before the fix the valid call is blocked, and after the fix it writes the requested replacement. This is a code-backed reproduction, not an assertion that this exact malformed payload occurred in the saved GunMayhem session.

The same normalization decoded all numbers as float64, conflating adjacent integer IDs above 2^53 and high-precision decimals. Both failure and successful-mutation counters use this identity.

## Change

Use encoding/json's sorted object-key serialization directly, preserving object/array types and avoiding the recursive intermediate pair-array tree. Decode with UseNumber so numeric spelling and precision remain intact. Retain the existing top-level advisory base_hash exclusion, key-order normalization, and empty/null argument behavior. Reject trailing input from normalization, preserving the original malformed evidence for normal validation.

Numeric spellings such as 1 and 1.0 now remain distinct rather than relying on float64 equivalence. This deliberately favors allowing a changed call over falsely blocking one. Tool execution and schema coercion are unchanged. The fingerprints are transient per-run state, not persisted keys needing migration.

No provider-specific path, model instruction, extra model call, repeated-read suppression, or automatic command retry is added.

## Validation

- Red/green tests distinguish object/array pairs, nested and empty structures, large integers and precise decimals; confirm key-order and advisory-hash behavior, malformed trailing input, and idempotence.
- Native/thin real patch recovery fails before and passes after the change.
- Existing repeated-failure/successful-write protections pass, including repeated successful real-file insertion.
- Full go test -timeout=90s ./..., go vet ./..., CLI and GUI builds pass.
- CLI --help exits successfully. Build artifacts and logs are retained inside the application folder.

## Local cost

Three 200 ms benchmark samples per version on the same machine:

| Fingerprint input | Before | After | Allocated bytes/op, before -> after |
| --- | --- | --- | --- |
| Small read arguments | 2.41–3.25 us | 1.95–2.15 us | 1,136–1,137 -> 1,729 |
| Twenty-change patch | 37.88–39.17 us | 30.29–31.78 us | 23,852–23,859 -> 21,666–21,673 |

The streaming decoder adds a small fixed buffer cost: small-call byte allocation increases despite fewer allocations (31 -> 29). The larger case removes 99 allocations (476 -> 377). These short local measurements are not an end-to-end provider latency claim. The principal benefit is allowing a genuinely corrected action to execute.

Artifacts: .tmp/repeat-argument-identity-2026-09-27/ (red/green, benchmarks, suite, vet, builds, smoke and installed hashes).
