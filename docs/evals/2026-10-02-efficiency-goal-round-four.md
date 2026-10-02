# Efficiency goal — fourth measured round

Date: 2026-10-02. Baseline: main `0cc12b9`. Goal remains active; this is a measured milestone.

## Changes

- TUI keyboard events go directly through the unchanged key-handling body. This avoids the general dispatcher receiver escaping to the heap through unrelated focus/blur code. Terminal batches, AltGr, busy input, questions, menus, draft recovery, copied style values and live style transforms retain their behavior.
- GUI SSE framing writes the already serialized JSON without building another payload-sized fmt buffer. The existing one Flush per complete event, JSON escaping, event order, cancellation and persistence remain unchanged. Actual HTTP/1 and TLS HTTP/2 parity and first-fragment delivery pass.
- The token estimator uses a 256-byte static lookup table for inputs below 64 bytes. Existing vectorized Count branches for longer text are identical. Estimates, rounding, native reasoning data and actual request tokens are unchanged.
- read_many accepts a unique singleton reads:{item:string} wrapper by preserving the already supplied shorthand. The normal parser, schema validation, sandbox and limits still apply. No path, range, edit anchor or intent is inferred.

No tool schema, prompt, provider request, timer, background service or dependency was added. Portable application storage and the special OpenCode Zen path are unchanged.

## Measurements and practical limits

Windows amd64, Ryzen 7 5800X3D, Go 1.26.2, GOMAXPROCS=2. These are synthetic component/request-preparation measurements with matched sources; no live provider/GPU test was required.

| Component | Before | After | Scope |
| --- | ---: | ---: | --- |
| Empty TUI Esc | 27.469 µs / 98,392 B | 17.548 µs / 49,240 B | One event, no command execution |
| TUI rune + Backspace | 82.551 µs / 201,761 B | 55.233 µs / 103,446 B | Two events |
| SSE HTTP loopback, 128 KiB result | 276.890 µs / 293,561 B | 254.014 µs / 148,073 B | Full server/client response |
| SSE HTTP loopback, 1 MiB result | 1.506 ms / 2,177,836 B | 1.429 ms / 1,169,796 B | Full server/client response |
| Short-text request preparation, English cold | 13.230 µs | 11.719 µs | 40-turn fixture, provider work excluded |

TUI saves exactly one 49,152-byte model allocation per ordinary key; status-refresh allocation is unchanged. GUI large-result framing roughly halves transient allocation in these fixtures. Small-frame timing overlaps, and the 4 KiB SSE median was slightly slower. The classifier durable short-input medians improve about 4–10%; larger exploratory gains did not reproduce and are not promised. Long text takes the same implementation and timings vary with the benchmark environment.

These are cumulative allocated bytes and CPU time, not measured RSS/peak RAM, WebView2 process memory, input-to-pixel delay, model prefill or provider TTFT. They do not establish universal whole-task latency reductions.

Additional real-model smoke tests passed on local qwen3.8-27b-uncensored and Zen space-bunny-free: one greeting request, then two requests and one successful read_many with correct order and final answer. The isolated actual Loop used a small toolset, not native GUI/TUI or task A/B. Kilo was incomplete because of 429; its four wire requests and a helper-only cancellation flaw are disclosed in [live smoke report](2026-10-02-goal-round-four-live-smoke.md). That fixture flaw was fixed and tested offline without changing production code or repeating live requests.

The portable session audit examined six bounded sessions: 2,980 tool calls, 106 read_many calls, one current scalar-wrapper rejection. Its ranges are valid and now produce the exact canonical result and order. A shell read also occurred in the same assistant response, so this proves a repaired rejection rather than a causal number of saved model turns. Canonical valid calls retain their original repair-free route.

Details: [TUI key dispatch](2026-10-02-goal-tui-key-dispatch.md), [SSE framing](2026-10-02-goal-sse-frame-copy.md), [short byte counting](2026-10-02-goal-whitespace-short.md), [wrapped read_many](2026-10-02-goal-read-many-scalar.md).

## Rejected approaches

A local focused textarea copy broke post-focus style replacement. Shared palette pointers would change supported copied-model style ownership. Neither was shipped. Scalar, bitmap and safe SWAR replacements for the long byte-counting branch were substantially slower than stdlib vectorized scans; the long branch stays intact. Historical patch_file failures often omit the old-text anchor, so automatic patch guessing was rejected.

The previous larger request-estimator scratch cache remains rejected: its small CPU saving did not justify additional allocation, lifecycle ownership and plumbing.

## Final verification and local binaries

- Final `go test ./... -count=1 -p 2` and `go vet ./...`: PASS after the scalar type guard.
- All 136 JS UI tests: PASS; all JS/UI sources remain byte-identical after that Go-only guard.
- Full-source hashes stayed unchanged through final checks and all ten final builds.
- GUI and TUI CGO=0 builds: Windows amd64, Linux amd64/arm64, macOS amd64/arm64, all PASS. Cross-compilation is not native runtime validation.
- Local Windows EXEs installed as `1.0.4-dev.6` with matching SHA-256. CLI version and both help commands PASS. Previous EXEs remain in portable ignored backups.

| Installed binary | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26,013,184 | 9adff36b25d95e48c1b2658a1399af472fdcc5a48f8fb8d95cdecc1e92a3887d |
| supercli-web.exe | 22,748,160 | d633e3bc04d98c783892f7ec251be126cfa75efcd1d1cfd60deeccc17ed3476b |

No stable release, tag or updater manifest is published by this round. The human Linux tester confirmed the earlier supplied package works; this is independent of cross-compilation and is not native execution of every new target.

Raw check, build, installation and source-hash records are retained under the portable ignored `.tmp/goal-round-four-2026-10-02` directory.
