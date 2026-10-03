# Text tool-call argument encoding

2026-10-03. Baseline dev19 5524acf. A multiline XML parameter contained raw JSON control bytes after the fallback parser escaped only quotes/backslashes. Hardening correctly rejected that invalid JSON and asked the model to repair an argument the model had already supplied. Sentinel values had the same problem for interior tabs/CR/NUL/ESC; key encoding could also emit object/array-shaped keys as raw JSON.

The corrected literal encoder escapes all JSON control bytes and quotes/backslashes, preserves valid UTF-8/HTML bytes, and uses encoding/json replacement semantics for invalid UTF-8. Keys always use literal quoting. Existing structured argument/blob handling and invalid-object rejection remain intact; native provider calls are untouched. Single-line sentinel values stay single-line.

Real offline Loop.Run with an owned read-only fixture: baseline 3 scripted provider calls, one rejected tool result, one successful exact-value execution; corrected 2 calls, no rejected result, the same one exact-value execution. All 32 interior control bytes, quoted/structured-looking keys, raw valid objects/arrays, malformed object rejection, UTF-8/HTML and sentinel controls passed. This establishes a forced repair removed in this synthetic case, not a universal live turn/token saving.

A first encoding/json.Marshal-every-literal prototype was rejected for additional HTML expansion and allocations. One bounded ABBA comparison per distinct prototype, GOMAXPROCS=2, Ryzen 5800X3D/Go1.26.2 Windows, actual XML extraction + HardenToolCall, 100ms/sample:

| Case | Baseline ns/op | Accepted literal ns/op | Baseline / literal allocs |
| --- | ---: | ---: | ---: |
| short | 653–676 | 706–769 | 10 / 11 |
| quotes/backslash | 804–806 | 787–790 | 13 / 13 |
| plain ~8K | 25,807–25,835 | 29,521–30,357 | 11 / 12 |
| code ~8K | 43,087–44,025 | 44,435–46,055 | 12 / 13 |

The added literal-key allocation is necessary for correct arbitrary keys. Accepted encoder stays ~27KB/~33KB per large successful pipeline, unlike the rejected marshal variant (~35KB/~55KB). This is a correctness fix, not a faster parser claim. Multiline baseline rejected early (13,979–14,005ns), whereas the corrected pipeline actually succeeds (37,902–38,616ns), so those timings are not equivalent completed work. Accepted multiline allocations 12 vs baseline rejection 24, B/op ~28,609–28,615 vs ~29,323–29,325.

Frozen negative/positive sources, overlays, terminal test/ABBA logs are ignored under .tmp/goal-text-call-json-2026-10-03. Permanent regressions include the actual tool execution without repair. No live model, program, image or private prompt was used.
