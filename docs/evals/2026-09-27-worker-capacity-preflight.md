# Reject full worker capacity before preparing another loop

## Evidence and scope

The fixed completed-session capture confirms that send_message continued the same GunMayhem worker from an incomplete report to a verified focused change. Inspection also confirms that child and coordinator result references already share the output store. Neither path was rewritten.

Current code did reveal unnecessary work for a task that cannot start: it performed repository preflight, the optional backend availability probe, child registry construction and NewLoop before TryAdd checked the active-worker limit. Four native/thin + created/running regressions record one preflight, one probe and one loop construction for an already-full registry. They fail before the fix and pass afterward.

This failure was reproduced from current code. No full-capacity refusal was found in the inspected completed-session capture, so it is not claimed as a cause of that session's long duration.

## Change

After validating the requested worker kind, task checks available capacity before preparing the worker. The same locked limit predicate remains in TryAdd at registration: the early observation does not reserve a slot and cannot bypass the final concurrency guard. Another delegation filling capacity during preparation is still rejected.

No new settings, tool schemas, model instructions or inference calls. Error wording and maximum-worker policy are unchanged. A rejected call no longer populates the backend-probe cache. Finished, failed and stopped workers free capacity as before. Nil and unlimited registries retain their existing behavior.

This does not queue requests, change worker scheduling or eliminate preparation when a slot becomes occupied after the early check. Accepted delegations pay one additional bounded registry scan.

## Measurements

Windows / Ryzen 7 5800X3D / Go 1.26.2, three 300 ms benchmark samples, real worker schema setup, no model/network latency:

| Local operation | Before | After |
|---|---:|---:|
| Reject task with full registry | 26.1–28.1 microseconds | 1.24–1.28 microseconds |
| Bytes allocated per refusal | about 21,751 | 664 |
| Allocations per refusal | 213 | 11 |
| Added available-capacity check, 20 finished + 5 active workers | not present | 0.307–0.313 microseconds, 0 allocations |

The refusal benchmark does not configure network probing or repository preflight. Their removal is demonstrated by call counters, not an invented wall-clock saving. These measurements do not imply a whole-session or provider speedup.

## Validation

- Regressions: full registry skips preparation; terminal workers free slots; final registration rechecks capacity after a competing task fills it; nil/unlimited registries continue to work.
- Existing active-limit and canceled-task checks pass.
- Full go test -timeout=90s ./..., go vet ./... and both CLI/GUI builds pass.
- CLI --help smoke check and diff whitespace validation pass.

Evidence and executable backups: .tmp/worker-capacity-preflight-2026-09-27/.
