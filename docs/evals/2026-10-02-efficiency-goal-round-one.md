# Active SuperCli efficiency goal: first integrated round (2026-10-02)

The goal remains active. This round integrates measured changes without new app dependencies, permanent model instructions, classifier/provider calls, automatic polling or an always-running service. Portable application data and the special OpenCode Zen transport remain intact.

## Integrated results

- [Image sampling](2026-10-02-goal-image-sampling.md): synthetic 4K PNG/JPEG preparation about 25%/21% faster with byte-identical encoded outputs. Prepared coefficients use 24 KiB more transient memory per measured image. A cancellation edge at the final sampled pixel is also fixed.
- [GUI/TUI](2026-10-02-goal-ui-residency.md): type/Backspace pair allocation 136 KB to 103 KB; compact by-value marker keeps tool-row allocation unchanged. Date labels reuse the existing bounded formatter cache. A pointer prototype was rejected because it increased tool-row allocation.
- [Thinking scan](2026-10-02-goal-thinking-scan.md): the actual completed_800 history projection fixture goes from 51.26 to 28.16 us and from four to two allocations. Plain text does not get a whole-answer lowercase copy just to search for reasoning tags.
- [Search schemas](2026-10-02-goal-search-schema.md): small model-facing discovery schemas use raw JSON instead of JSON inside a JSON string. Public output is unchanged. Historical eligible responses shrink about 16%; actual deterministic HTTP/loop fixtures retain three requests and the same executed result.

These measurements cover their named synthetic CPU paths and serialized bytes. They do not establish lower WebView2 process RSS, whole-turn latency, real prompt-token savings or fewer model turns for an arbitrary coding task.

The log audit found a recent long session with 177 model calls and 250 tool calls (226 ctx_execute), with approximately 93.4% of input usage cached. Call count alone is not evidence of an unproductive loop. No punitive repetition counter or extra instructions were added.

## Deferred candidates and next priorities

- The unused Palette.System field saves 552 structure bytes but leaves Model in the same 48-KiB allocation size class. It was not changed for a speculative performance benefit. Other palette fields are used or must remain independently customizable.
- The DormantImages no-image shortcut saves only 48 bytes and about 33 ns while touching writer aliasing. It was not implemented.
- Next audit priorities: stable request-prefix/context reuse and retained data across long sessions or inactive workers. Changes require measurement and equivalent restoration/continuation behavior; discarded history is not a RAM optimization.

## Integrated validation and installation

- All packages pass go test ./... -count=1 -p 2.
- go vet ./... passes.
- All 133 JavaScript UI tests pass.
- SHA-256 guards confirm implementation/test sources stayed unchanged through final tests and builds.
- Ten stripped CGO-disabled builds pass: TUI/GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Cross-build success does not mean native runtime verification on those platforms.
- Windows TUI/GUI are backed up and replaced; installed hashes match their build artifacts. CLI reports 1.0.4-dev.3; GUI help exits successfully.
- No new public release or updater manifest was published.

The user separately relayed successful use of the previous Linux development package 1.0.4-dev.2 by the person reporting the first-run selector issue. No private session content is included in this report.

Artifacts: .tmp/goal-round-2026-10-02/full-checks.json, builds.json and installed.json. Per-change reports link their original measurements.
