# Avoid a formatting-only patch retry

Date: 2026-09-27

## Evidence

A one-time read-only transaction captured the latest twenty completed turn summaries and their bounded transcript ranges in .tmp/completed-turns-2026-09-27/. No active-session progress was polled.

The new Qwen conversation has five distinct tool calls in its project-review turn and no tool failures. It took 100.579 seconds with recorded stream_total 79.838 seconds, backend_wait 16.523 seconds, tool_execution 3.737 seconds and context_prepare 0.013 seconds. Phase counters can overlap; they are not summed here. The trace does not justify suppressing a repeated read or claiming that CLI preparation caused most of the delay.

Later completed GunMayhem records expose another avoidable retry. At sequence 1587, patch_file repeats the same target path at the root and inside changes[0]. The schema rejects that inner path at 1588. The call at 1589 removes only that field; old/new bytes and the target are identical, and the patch succeeds at 1590. Local structural comparison confirms the repair consists solely of deleting the matching inner field. Private source and captured arguments stay in local artifacts.

## Change

The tool registry supports an optional pure RepairArgs callback only after schema rejection. A candidate is coerced and revalidated against the original schema; otherwise the original failure is returned. Valid calls never invoke the callback. No schema or description sent to the model has been expanded.

PatchFile opts in for one narrow case: remove path inside changes[] when an explicit nonempty root path exists and every supplied inner path equals that root exactly as a decoded string. No path spelling, case or relative-path normalization is attempted. Missing roots, conflicting targets, duplicate JSON keys and malformed changes decline repair. The original raw request is passed to the repair callback, so ordinary scalar coercion cannot first erase ambiguous duplicate keys.

Remaining unknown arguments and schema constraints must still pass validation. Normal patch execution retains sandbox checks, expected-count and base-hash enforcement, exact replacement semantics and atomic writes. Repair does not retry execution and cannot write a file itself. Worker registry copies retain the callback alongside the existing compiled validator.

## Verification

- Four native/thin × original/copied-registry coding fixtures fail before and pass after. One patch executes, with one tool-generating request and one final-report request; no formatting-repair turn is needed.
- The exact saved first request is replayed locally against its old text in an isolated temporary file. It produces the same new bytes as the recorded successful retry, without resending the request. This opt-in test keeps private source out of checked-in fixtures and provider requests.
- Registry guards reject malformed/invalid repair candidates, retain scalar coercion, and prove valid requests never invoke repair.
- Real-file guards cover repeated paths, mixed optional presence, deletion, conflicting paths, missing roots, alternate path spellings, duplicate keys, unknown fields, wrong types, stale hashes, expected counts, later failed replacements and an attempted sandbox escape. Rejected edits leave both target and neighboring files unchanged.
- Unicode, quotes, newlines, backslashes and replacement text that itself mentions path are preserved.
- go test -timeout=90s ./..., go vet ./..., CLI/GUI builds, CLI --help and diff checks pass.

## Cost and limits

There is no additional fixed instruction, model request, file read or provider-specific branch. The special Zen path is untouched. Normal valid calls take the existing coercion/validation path; the callback runs only on a rejected request for a tool that opted in.

A local validation-only benchmark measures about 4.03–4.24 microseconds / 2626 B allocated for the valid fixture, 16.4–18.4 microseconds / 10231 B for the redundant-path repair, and 12.7–13.0 microseconds / 7486 B for a declined conflicting-path case. These figures exclude filesystem editing and provider inference. They are not a live latency comparison; the exact replay establishes that this particular formatting retry is unnecessary.

Artifacts: .tmp/redundant-patch-path-2026-09-27/ contains before-source copies, private saved arguments, red/green/guard tests, benchmark output, full validation, builds and installation hashes.
