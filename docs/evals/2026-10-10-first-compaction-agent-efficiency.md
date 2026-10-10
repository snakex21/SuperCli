# First compaction and avoidable agent work

Date: 2026-10-10. This follow-up preserves the user's thinking selection, provider, output budget, instructions and existing history policies. Application data, test fixtures, temporary directories, caches and executable backups stay inside the application/repository directory.

## First Qwen compaction

The first helper already makes one completion request with two messages, an 870-byte system instruction and the selected transcript. It offers no tool schemas. The recorded coding fixture's helper transcript is 25,996 bytes. There is no duplicate helper request to remove at this boundary.

An offline diagnostic using that completed fixture measured `CompactNow` through entry to `Complete`: median 17.45 microseconds, with a separate final verification at 12.62 microseconds. This includes window lookup, history/request projections, prefix selection, eligibility checks, transcript rendering and immediate probe-error cleanup. It excludes loop construction, GUI/config/network work, factory metering, HTTP serialization, inference and post-summary persistence. The callback checks exact system and transcript bytes, zero tools and exactly one callback; failure leaves history byte-for-byte unchanged. These are CPU preparation timings, not GUI wall latency.

An experimental test-only JSON whitespace pass saved just 2 of 25,996 bytes. Its copying adapter increased CPU and allocations; it was rejected and is absent from production. No new model inference was justified because there was no meaningful input or execution change to compare. The production prompt, chosen effort, thinking toggle, output budget and compaction history are unchanged.

The preceding isolated live measurement remains useful context, not a new benchmark: helper EOF at 12.885 seconds, first output at 1.396 seconds, first summary content at 3.595 seconds, 411 output tokens including 76 reported reasoning tokens. The earlier 60.046-second run included both a 38.845-second helper and a 21.200-second main continuation. Waiting includes transport/backend scheduling, so it must not be labeled pure prompt processing. Neither unrelated run establishes a speedup from this patch. See [the earlier evaluation](2026-10-10-qwen-timing-and-currencies.md).

Receipts: `.tmp/compact-first-oct10/setup.json`, `argument-trial.json` and `final-verification.log`. The opt-in diagnostic saves counts, hashes and times without new private reasoning or raw transcripts.

## Preflight allocations

Git porcelain parsing now streams lines and retains only the existing small exact listing/sample. It no longer allocates two full line slices for an arbitrarily large dirty tree. Status kinds, area ranking, filename display, thresholds and generated briefing are unchanged, including quoted renames, Unicode and malformed byte sequences.

| Canned whole briefing fixture | Previous median | Current median | Previous allocation | Current allocation |
| --- | ---: | ---: | ---: | ---: |
| 6 paths | 4.397 microseconds | 4.425 microseconds | 4,202 B | 4,154 B |
| 500 paths | 94.071 microseconds | 80.538 microseconds | 56,108 B | 30,188 B |
| 50,000 paths | 8.592 milliseconds | 6.294 milliseconds | 7,259,819 B | 2,113,898 B |

The large canned fixture reduces preparation time by about 27% and allocations by about 71%. The small timing difference is noise. Native Git timings varied around 45–63 milliseconds and do not establish a whole-turn speedup. Native cold execution still uses two Git processes; the working-tree status remains fresh. The existing two-second static log cache is unchanged and can briefly retain earlier HEAD/history after a rapid commit; this change does not introduce a partial repository fingerprint or extra Git processes.

Tests compare the original implementation against the new parser byte-for-byte over 2,000 randomized cases, threshold 16/17, blank/CRLF lines, quoted paths, renames and invalid UTF-8. Hard token budgets and changing task state are checked. Receipts and frozen baseline: `.tmp/preflight-turn-speed/receipt.json`, `preflight.before.go` and `baseline-overlay.json`.

## Preserve completed operations across discovery

Standard tool discovery only activates existing registry metadata, yet it was treated as an unknown side effect. Consequently it discarded previously verified output receipts and accepted read evidence, including a worker's parent receipt. A later identical operation could execute the effect again.

The standard search registration now explicitly certifies that its metadata effects preserve evidence. Unknown tools and real effects still invalidate evidence. The capability does not authorize concurrency, cached discovery, installation or contract replacement. Result TTL reuse independently requires `ReadOnly`; a non-read-only metadata tool cannot acquire cached results merely by setting a TTL.

On an identical completed operation, its registered checker still verifies current permission and actual state. Changed/denied output cannot be overwritten or reported complete; missing output takes normal execution. A second distinct path/URL is a distinct operation. New user instructions, new runs and registry replacement clear old receipts. The model remains responsible for requesting all outputs, inspection and its final reply; this patch does not force completion or claim fewer model requests.

## Reuse the final context calculation

At the final answer, the loop previously built its resolved context to decide whether saving was needed, then calculated the same view again inside the save. It now passes that just-computed view directly to the existing save path, and skips preparation altogether when the writer cannot store a projection.

The prepared view exists only at that completion boundary; it is not cached across turns. A failed write marks the projection dirty, and recovery rebuilds from the latest history, including new instructions and unfinished tool evidence. Existing image handling, leading-system stripping, persistence-health guards and the lossless conversation archive remain unchanged.

Validation includes actual session storage and a complete loop response, exact archived-prefix equality, the saved provider view, Unicode corrections and active failed tool evidence during recovery, and zero allocations with an unsupported writer. Comparative measurement and final build receipts are recorded under `.tmp/first-compact-efficiency/`.

On a 400-chain fixture, median completion-boundary preparation fell from 49.286 to 25.529 microseconds; allocation fell from 198,400 to 99,200 bytes and from four to two allocations. The benchmark uses a lightweight save callback, excludes SQL/disk work and establishes no model/whole-turn latency claim. Receipt: `projection-bench.json`; raw samples: `projection-bench.log`.

## Validation and installation

- All 19 Go targets passed: account FX/usage, session storage, LLM/factory, config, preflight, stats, GUI, TUI, app, agent, tools/core/search/web/sandbox and both commands (the CLI command has no test files).
- All 301 Node UI tests and `go vet` for the same Go targets passed.
- First-compaction opt-in offline validation passed three tests and its benchmark. It made no inference and left the original history unchanged.
- Focused discovery/replay validation passed 29 top-level tests. Actual download tests use the registered download/search/replay functions, real saved files and a fixture HTTP transport; whole-loop lifetime and final-answer behavior are separately checked with a deterministic provider. No unsafe reflection, network-policy change or website-specific branch was added.
- The initial expanded suite caught an error in the newly strengthened archive test: `ReadMessages` returns `[]session.Encoded`, while the test compared it with `[]llm.Message`. The corrected test compares every stored archive row before/after the loop. The other 18 targets passed initially; the full agent package passed after this test-only correction. UI tests and vet then completed. Both runs are retained as `go-tests.log` and `go-tests-agent-retry.log`.

Checks: `.tmp/first-compact-efficiency/checks.json`, `go-tests.log`, `go-tests-agent-retry.log`, `ui-tests.log`, `vet.log`. Both executables were built from a stable source manifest, installed by atomic replacement, and verified by installed `--help` exit-zero smoke tests and matching SHA-256 hashes:

| Installed executable | SHA-256 |
| --- | --- |
| `supercli.exe` | `85CB3D0C1F47BAD268284A485C30F0BCA721DFC76D0CCD8271BF88795C8CE39D` |
| `supercli-web.exe` | `6E8B5B026C8D3C5D9A3A85AD934F7568719D54A5DE8391C5995B517DD956500D` |

Previous binaries remain in `.tmp/first-compact-efficiency/backups/035b3ffa-f640-44cc-9902-43e701c6840f/`. Build/install/smoke receipts are in that stage directory. No GUI layout/assets were changed during this follow-up, and no running user process or configuration was modified. Restart the application to load the installed code.
