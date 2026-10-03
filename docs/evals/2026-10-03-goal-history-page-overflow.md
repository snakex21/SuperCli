# Release the excluded row from paged history

Date: 2026-10-03. Base: main 547c626 (dev18). Local development build: 1.0.4-dev.19.

## Confirmed retention and change

ReadMessagesBefore fetches limit+1 rows so it can report older history without a separate COUNT query. It removes the extra row from the returned slice length, but previously left that Encoded record in the backing array. Its content, parts JSON and tool-call JSON could therefore remain reachable for the lifetime of the returned page even though they belonged to no visible record.

The existing page slice now clears only out[limit] immediately before shortening it. No message is deleted or truncated in SQLite. The same query, snapshot, row scanning, error behavior, context handling, ordering, requested bounds and hasMore calculation remain. The selected page payload and transcript/model context are unchanged. No cache, timer, service, dependency, schema, prompt instruction, provider request or OpenCode Zen route is added or changed.

This path backs the GUI transcript history endpoint; ordinary TUI resume uses other reads. The effect is conditional on a sizable record immediately outside a page. It does not establish lower idle/startup WebView2 RSS, faster inference, lower model token use or fewer agent turns.

## Completed evidence before production port

Two separate owned Go test processes used real portable SQLite stores and baseline/candidate overlays. The proof produced 63 page/before/limit combinations, including default, negative and capped limits, empty pages, missing sessions, Unicode text, tool call/result IDs and explicit image provenance. A cancelled query retained its cancellation error. Both arms returned identical complete JSON bundles: 37,898 bytes, SHA-256 3cee004a30298aeb55a29944a8f79854a02dbe3f3796dbab655ebebbcb554995.

An additional real SQLite fixture stored an 8 MiB text part in the older user message and a small latest answer. ReadMessagesBefore returned only the answer with hasMore=true. After closing SQLite, forcing GC and keeping the returned page alive:

| Measurement | Baseline | Candidate |
| --- | ---: | ---: |
| Visible records | 1 | 1 |
| Hidden payload bytes reachable through the backing array | 8,388,648 | 0 |
| Go HeapAlloc after GC | 8,884,728 B | 488,352 B |
| HeapAlloc delta from process pre-fixture snapshot | +8,227,408 B | -168,656 B |

The exact excluded payload reference is eliminated. Heap figures include other runtime allocations and are one sample per arm, not a timing/RSS benchmark or a general session-size forecast. The initial probe failed to compile because its synthetic tool-call Arguments field used RawMessage instead of the actual string contract; that fixture was corrected before either measured arm ran. Production source remained unchanged during the completed proof. No live model, screenshot, user app or user profile was opened.

Ignored proof: .tmp/goal-page-overflow-2026-10-03, including both overlays, the actual SQLite/GC probe, completed logs, measurements.json and before/after source manifests. The existing meaningful storage paging tests remain; no production test mirroring the one cleared slot was added.

## Integration and local state

- Full go test ./...: 71 test-bearing packages PASS and 28 without tests.
- go vet ./..., owned-source formatting and git diff --check: PASS.
- Ten CGO-disabled CLI/GUI cross-builds passed: Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Cross-builds do not verify runtime behavior on those other operating systems.
- All 1850 cmd/internal/test and module-file hashes match before tests, after tests, after builds and after installation. GUI JavaScript/test sources are unchanged from the preceding 188-PASS UI suite; that unchanged suite was not repeated for this one Go storage change.
- Installed TUI --version reports supercli 1.0.4-dev.19; --help succeeds. The GUI artifact is staged. A previous dev18 replacement received Windows EPERM while the app was open; the dev19 GUI replacement was not attempted while waiting for normal user closure. The current installed GUI remains verified dev17. No application was killed, closed, restarted or activated.

| Windows dev19 artifact | Bytes | SHA-256 | Local state |
| --- | ---: | --- | --- |
| supercli.exe | 26313728 | 964e3fdbffbe1461f853fe1922eb58a583eb30cb2d3519dcbb0d9d93985df15d | installed; dev18 backup preserved |
| supercli-web.exe | 22999552 | 27027cc0e46660de36c25b0e7a98cb5a8fd2552c7141d87dcdc6ab622c2c87eb | staged; installed GUI remains dev17 |

Both Windows file sizes are unchanged from dev18. Existing toolchain/module files were read only; all fixture/cache/temp/build/backup and receipt writes stay under the repository. Installation is split so TUI can update without reattempting the locked GUI. Root receipts: .tmp/goal-integrated-dev19-2026-10-03. This is a development build, with no new public tag/release.
