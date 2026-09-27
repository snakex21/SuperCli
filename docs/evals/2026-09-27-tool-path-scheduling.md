# Preserve file-operation order across path aliases

## Reproduced defect

The file-batch scheduler compared normalized argument strings. A relative write to a.txt and an absolute read of the same file appeared independent and ran concurrently. An isolated real-filesystem replay returned the old content after a successful write in both native and activated invoke_tool/thin cases. This is a current-code reproduction, not an attribution of the long GunMayhem session.

The planner defect also reproduces with a project-root directory operation, a newly created target, copy/move paths, hard links and symlink parents. Mutation locks serialize writes but do not establish model call order.

A second real-filesystem regression moves one hard link to a previously absent path, then writes that path and reads the other link. Resolving all identities only before the first wave still returned old content. The later wave must account for the completed move.

## Change

Known file accesses resolve against the loop workspace. Shared parents are canonicalized once per batch; Lstat checks the final component, and final symlinks are resolved as well. Existing file identity checks detect hard links. Paths keep meaningful whitespace. Missing targets use the resolved parent. Resolution errors or a missing workspace use the existing conservative execution fallback.

Later groups of potentially parallel operations are checked again after earlier groups execute, so a move cannot leave stale identities. The first group uses the already computed plan. There is no persistent identity cache. Independent files still run concurrently, results retain call order, and read-only batches keep their existing fast path.

No new model request, schema field, prompt instruction or provider policy. The special OpenCode Zen path is unchanged. GUI, TUI and workers share this scheduler.

## Tests and measurements

- Same-resource matrix: relative/absolute paths in both directions, writes, read-before-write, new files, root and normalized paths, copy destinations, move sources, hard links.
- Symlink parent to a not-yet-created file; final symlink to a whitespace-prefixed filename. Both symlink tests ran successfully on this Windows machine.
- Native and thin/activated-dispatcher integration: the real write succeeds, the following real read returns the updated content, and tool-result IDs remain ordered.
- Metadata changes between batches and the move/write/read hard-link regression.
- Existing tests confirm independent writes/reads, worker policy, command barriers and cancellation behavior. The independent-write fixture now supplies its workspace explicitly.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds pass.

Windows / Go 1.26.2 / Ryzen 7 5800X3D; three 300 ms samples. Planning four mixed file accesses costs 7.3–8.7 microseconds before (incorrect lexical independence) versus 549–605 microseconds after (filesystem identities), with approximately 5.0 KB -> 17.8 KB of transient allocations. The extra local cost is about 0.6 ms for this fixture and does not apply to all-read batches. Resolving every full path separately was measured at about 2.5 ms; sharing canonical parent checks reduces that overhead. This is a correctness repair that can avoid misleading evidence/rework, not a measured reduction in live model requests or whole-session time.

## Artifacts

.tmp/tool-path-scheduling-2026-09-27/ contains original files, regression results, benchmarks, full validation and executable checks. The authoritative red run for the complete native/thin fixture is tests-before-corrected-fixture.json: the initial thin fixture had not activated its mutating tool, which was corrected before repeating the baseline. move-before-recheck.json independently reproduces the stale identity after a move.
