# Universal preflight, compaction and result reuse

Date: 2026-10-10. Follow-up to the request preparation and session-speed fixes. Changes apply to shared code, without model/provider/site detection. User-selected reasoning effort, thinking toggle, provider, summary prompt and conversation agreements must remain intact. Application/test data and executable backups remain inside the application directory.

Validation and measurements are recorded under `.tmp/universal-followup-preflight/`, `.tmp/universal-followup-compact/`, `.tmp/universal-followup-operations/` and `.tmp/universal-followup-efficiency/`. CPU setup and allocation measurements are separate from inference time; they do not establish a faster model-generated summary.

## Preflight

Implemented: keep the executable resolved by the existing per-collection Git lookup, rather than asking both native commands to resolve Git again. No persistent executable-path cache or additional subprocess is introduced. Working-tree status remains fresh; the existing two-second static identity cache is unchanged. The native runner preserves original argv[0], root, command arguments, environment, hidden window, shared deadline and WaitDelay; custom runners remain authoritative.

An independent frozen runner compares exact briefing bytes for 0, 6 and 200 changed paths, then immediately edits a tracked file and compares again. Tests also cover lookup errors (ErrDot/not found/permission), changing PATH between collections, custom runner arguments, cancellation, existing randomized porcelain parsing, budgets and fallback limits. Full preflight package passes in 4.669 seconds before combined integration checks.

Three serial 200 ms samples on Windows/amd64 measure command preparation with no subprocesses: median 19.661 → 6.132 ms on the current PATH, 224,840 → 77,272 allocated bytes, 2,552 → 870 allocations. The portable fixture with 24 search directories measures 6.470 → 1.902 ms. Resolution count is three → one. These depend on local PATH/filesystem cost and are not model latency.

The separate whole native cold collection remains two Git commands; median 49.182 → 48.974 ms is within run-to-run noise. It does not establish a faster whole preflight/turn. Whole-collection allocations decrease from approximately 392 KB to 242 KB. Detailed samples, frozen source, hashes and full tests are in the preflight receipt.

## Compaction

Rejected after measurement: avoid decoding arguments and tool-result bodies for operations which the existing compact file-fact collector never uses. Both prototypes preserved exact facts/history/helper input and improved large unrelated command arguments, but added overhead on ordinary small file operations. Production `context_file_facts.go` remains byte-identical to the frozen baseline; neither prototype is shipped.

The first prototype used a selector before the existing dispatch. An isolated interleaved comparison measured the recognized-small case at median 9.916 → 10.862 microseconds, with unchanged allocated bytes/count. The second prototype classified once into existing fact-operation groups. Its paired recognized-small comparison measured 12.798 → 16.297 microseconds; small wrapped invocation measured 28.347 → 29.335 microseconds with substantial scatter. It is not justified to trade an ordinary-path slowdown for a synthetic large-argument gain in this universal follow-up.

Independent expected-outcome tests cover the existing file operations, direct and wrapped invocation shapes, malformed/error/blocked/mismatched results, ambiguous duplicate call IDs, user boundaries and unknown/worker calls. A helper test checks exact prompt and transcript, one completion call, two messages, zero tools, preserved constraints and unchanged canonical history. Recorded coding input has 13 messages, 25,996 helper-input bytes and 242 fact bytes; baseline/candidate hashes match. The ordinary production implementation is included in final integration checks.

No shorter prompt, altered reasoning effort, lower output limit, removed agreement or speculative summary is introduced. This round does not establish a further compaction speedup. Prior timing evidence separates client setup (microseconds) from helper/model generation (seconds); the latter remains the substantial part of the observed compaction latency. Full rejected-prototype evidence remains in the compact artifact directory.

## Reusable observations

Implemented candidate: retain recently used results when the existing bounded read-only result cache is full. Cache hits change eviction order only; they do not refresh creation time or TTL. The newest entry requires no order scan; other hits move at most 16 keys without allocation. Existing caps remain 16 entries, 1 MiB total, 128 KiB per entry and two-minute maximum TTL. This trades a small bounded bookkeeping cost for fewer repeat tool executions under cache pressure.

Code review also found a pre-existing reference-sharing issue: Result.Operation contains opaque effect evidence. Such effect-bearing results are now excluded from the observation cache, like existing retained/media/command results. A regression test mutates an opaque evidence map and checks the second call executes the tool and observes the updated value, rather than reusing an effect result. No generic deep copy of unknown evidence is attempted. The separate verified-operation replay and its revalidation are unchanged.

The TTL contract remains an explicitly registered, trusted, short-lived read-only observation. Arguments are validated on hits, but arbitrary live permissions inside the tool function are checked only when it actually executes after refresh/expiry/invalidation. This change does not add a new live-permission mechanism to TTL hits. Tests check refresh, effects (including failure after a state change), queued instructions, new runs, registry replacement, cancellation and output-store handles. Model tool-call/result protocol pairs and provider calls remain intact.

A frozen pre-edit overlay reproduces eviction of an active query after 15 other queries, an active hit and one further query. For 17 distinct arguments it executes the active query twice (18 total actual function executions); current code executes it once (17 total, 5.56% fewer). This is a reduction of actual tool-function work, not fewer model-selected tool-call messages. A scripted real loop with a second independent goal keeps all seven provider calls, finishes both goals and preserves the final answer across unrelated tool/provider names.

Three serial 300 ms samples compare frozen/current source. A newest-entry hit remains allocation-free, 40.79 → 39.66 ns median (noise). Rotating 16 older entries costs 33.95 → 51.55 ns median, an explicit 17.60 ns bookkeeping cost with zero allocations. Actual pressure dispatch measures 205.930 → 184.491 microseconds median, 123,083 → 121,101 bytes and 1,506 → 1,475 allocations; overlapping timing samples do not establish a robust whole-dispatch speedup. Every sample confirms actual function executions 18 → 17. Fixtures use deterministic cheap tools, without simulated network delay or inference.

The final focused suite passes 31 top-level tests, including existing verified-receipt live authorization/state/multiple-output tests. Raw before/candidate data, the original source, red reproduction, correctness and hashes remain in the operations receipt directory.

## Final validation

Combined validation passes on the first run: Go tests for all 19 targets (the CLI command has no test files), 309/309 UI tests with no skips/failures, and go vet for all 19 targets. The normal full agent suite includes the new compaction helper/outcome tests; archived coding parity is separately validated through opt-in fixture receipts. Canonical compaction fact source SHA256 remains A0A4C198E24ADA36CA9126F4492697F8BEFC1113F6DF28FFAFB6D62EF6E5106E.

Both executables were built with source hashes stable before/after compilation, then atomically installed with verified previous-binary backups. Installed --help smoke tests pass for both. Build, source manifest, installation and smoke receipts are in `.tmp/universal-followup-efficiency/`. Backups are in its `backups/2a9aa836-0b87-48a3-952f-3178010a4325/` folder. No Git commit or push was made. Restart a running CLI/GUI to use the new executables.

| Installed executable | SHA256 |
| --- | --- |
| supercli.exe | 789D1DF3449D1F453A8D85D17E72144DFE09D999BEAE52642560C86D93D02510 |
| supercli-web.exe | 76C1587BA94617D30255FB28600E1065E9C4E31231BA38A16E8DDD673A25EDF0 |

No GUI source, user preference or model configuration is changed in this follow-up. The previously restored persisted average generation-speed panel remains intact.
