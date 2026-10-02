# Efficiency goal: second integrated round (2026-10-02)

Baseline main `3eae0a5`. Local Windows GUI/TUI builds: `1.0.4-dev.4`. This round changes derived tool definitions, result rendering and duplicate storage, while preserving conversation evidence and provider requests. No new provider instructions, dependencies, model calls, daemon or special Zen changes.

## Accepted changes

- [Versioned tool definitions](2026-10-02-goal-tool-definition-snapshot.md): one latest revision-keyed snapshot and its existing estimated token cost. Providers receive a fresh independent slice. Warm preparation fixture improves 27.86 to 8.74 us (full tools), with 35.6 to 13.3 KB allocated per sequence. Cold complete preparation also improves; standalone cold builds add about 1–4.4 us. The fixture retains 3024 bytes of descriptor backing storage plus 88 bytes of state; this is an explicit bounded tradeoff, not a claim of reduced process RSS.
- [Large TUI results / terminal GUI flush](2026-10-02-goal-large-ui-render.md): only visible result lines are split, and plain text avoids failed JSON parsing. The roughly 1-MiB TUI log fixture improves 273 to 34 us and about 1.3 MB to 4.8 KB allocated per render. GUI repeated done/EOF/seal operations render the same complete source once instead of three times; three scrolling calls remain. Existing DOM identity and HTML are unchanged. No FPS, GPU or WebView2 working-set measurement was made.
- [Saved output restore](2026-10-02-goal-output-restore.md): immutable SQLite output is requested as TEXT from the driver without changing persisted BLOBs or byte limits. A 1-MiB database read avoids about one MiB of transient Go copies; measured mean read time 568 to 434 us. All byte values, NUL, legacy bytes, restart/deletion/eviction and cancellation remain covered. Warm cache behavior is unchanged.
- [Finished worker reports](2026-10-02-goal-worker-report-residency.md): exactly matching final report text shares the existing history string. Full history/native state remains for continuation; failures, multipart results and diagnostic suffixes retain their old storage. Large synthetic 20-worker fixtures reclaim about 1.4/21.2 MB heap for 64-KiB/1-MiB reports. These stress results do not predict typical user-session RAM savings.

The affected Linux user confirmed that the earlier `1.0.4-dev.2` package works. The [headless/terminal report](2026-10-02-headless-runtime-terminal.md) now records that external confirmation; it is separate from native testing performed here.

## Verification and installation

- Full `go test ./... -count=1 -p=2`: passed.
- Full `go vet ./...`: passed.
- Complete JavaScript UI suite: 136 passed, zero failures.
- Independent snapshot review found no contract/ownership invalidation issue and corrected the resident-memory metric to count capacity.
- SHA-256 guard for 1752 source/config/assets files: unchanged through full checks.
- Ten CGO-disabled cross-builds: GUI/TUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64, all passed. Cross-build success does not establish native runtime behavior.
- Windows GUI/TUI copied into the application directory with matching SHA-256 and previous binaries backed up under the project .tmp. CLI `--version` reports `1.0.4-dev.4`; both supported `--help` commands exit successfully. The GUI has no `--version` flag; it was not added in this optimization round.
- No public release/tag/update manifest published, no model/GPU/live desktop/VM request used. Temporary artifacts and test state stay under the application project.

## Next measured/auditable candidates

The goal remains active. Long hidden/projected history still incurs repeated full scans. A separate read-only TUI audit found that appending completed messages invalidates all completed rendering; a retained immutable rendered prefix could help, but needs exact width/language/folding/copy semantics and separate viewport costs measured first. Existing completed-worker loops cannot be unloaded safely without a durable full snapshot; this round does not discard their state. No unmeasured broad history cache or worker unload was introduced.
