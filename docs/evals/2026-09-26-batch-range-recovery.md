# Bounded recovery for oversized batch reads

Date: 2026-09-26. Shared read_many tool, used by local/cloud models and native/thin tool calling.

## Observed problem

The fixed GunMayhem snapshot under `.tmp/session-audit-2026-09-25/` contains 24 range-cap failures in 11 read_many calls. Several are inclusive off-by-one ranges such as `300-600`: 301 lines requested against a cap of 300. A later repair attempt repeats the same shape, resulting in another error without any file contents for that item.

For example, sequence 1378 requests `netplay.go:1-300 | netplay.go:300-600 | netplay.go:600-900 | netplay.go:900-1200 | netplay.go:1200-1500` (with the original project-relative prefix). Only the first range was accepted. This is evidence of avoidable tool errors, not proof of 24 wasted provider turns: calls and independent reads can be batched.

The current thin Muse coding trace was also inspected. Its repeated read follows a test command, and the complete earlier read was still present in history. No automatic suppression or additional instructions were added for that case.

## Change

Like read_lines, read_many now reads a bounded prefix of an oversized, otherwise valid range. It still reads at most 300 lines per item, with the existing file-count, per-line and total-output bounds.

`300-600` now yields lines `300-599` and the explicit marker `[range capped at 300 lines; requested lines 600-600 not read]`. The section heading reflects the effective range. If EOF is reached inside the bounded range, the tool reports EOF rather than a false unread tail.

Missing files, invalid/inverted ranges, sandbox restrictions, cancellation and glob expansion limits remain errors. The default end also saturates at MaxInt rather than overflowing for an extreme start value. Valid requests follow the existing read/render path. There are no added model calls, tool-schema bytes, permanent instructions or cache layers.

## Verification

- Before/after regressions reproduce a 301-line request through shorthand, JSON-array input and glob expansion.
- Short EOF, huge requested ends, partial failures, cancellation and default-end integer overflow are covered.
- Long-line fixtures verify the existing output bounds and retain cap markers in visible, stored and model-preview output.
- The production Loop delivers bounded evidence and the unread-tail marker in the next model request through native and thin protocols.
- `go test -timeout=90s ./...`, `go vet ./...` and both executable builds pass.

The Loop test uses a scripted provider to inspect the actual outgoing evidence; its two requests are not a measurement of real-model turn savings. No provider latency claim is made. Large incomplete ranges can still require a continuation read; the improvement is receiving useful bounded evidence instead of an avoidable range error.

Raw red/green results, the fixed-snapshot error census, full validation and installation hashes are kept under `.tmp/batch-range-2026-09-26/`.

CLI and GUI binaries were installed after validation, with previous versions backed up and installed hashes checked. Restart existing instances to use the updated tool.
