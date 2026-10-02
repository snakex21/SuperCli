# Goal evaluation: singleton shorthand read repair

Date: 2026-10-02. Audit baseline: main 0cc12b9.

## Change

read_many now accepts reads:{"item":"file:from-to | file:from-to"} by unwrapping the existing unique singleton object. The raw JSON string becomes the ordinary reads scalar; its literal content is preserved. There is no path guessing, trimming, range inference, new quoting syntax, wider schema, permanent prompt, model request, service, dependency or Zen-provider change.

The common valid argument path does not invoke RepairArgs. Existing string-list normalization remains unchanged. Duplicate keys, extra root/wrapper fields, unknown wrapper names, blank/null/numeric/boolean/object values and malformed lists still fail. Array elements containing | remain ambiguous and rejected. The entire wrapped scalar is already the existing shorthand: its delimiters, Windows drive paths, whitespace, Unicode and quoted-path behavior pass through the same parser and sandbox/caps.

The singleton item's type is checked before attempting string decoding. uniqueArgumentObject consumes each value with json.Decoder.Decode into json.RawMessage, whose boundary starts after valid JSON whitespace. A length/first-byte quote guard therefore leaves the existing wrapped-list path without an extra failed string decode. Regression fixtures cover all four JSON whitespace characters around both scalar and list values, literal string whitespace, duplicate/competing fields and unchanged ranges.

## Saved-session evidence and limits

Read-only inspection of six bounded portable sessions, including FerrumScope/GunMayhem, covered 2,980 tool calls and 106 read_many calls. Private prompts, code payloads, credentials, paths and session IDs are not exported.

One October 2 call supplied three ranges inside a singleton item:string wrapper and was rejected before reading. Two older singleton item:[]string failures are already covered by the existing list repair. The scalar rejection was reproduced against the current production registry.

This establishes a rejected usable read, **not an additional real model turn**. The observed assistant response also contained a shell read. Aggregate input totals include history/cache and do not establish uncached processing. Repeated build/git commands generally followed edits; their count is not proof of loops. Historical patch failures commonly lacked a usable old anchor, so no patch-argument guessing was introduced.

## Local harness comparison

The nearby pi-main/packages/coding-agent/src/core/tools/edit.ts implements prepareEditArguments: a supplied singleton edit can become an edit array, and a stringified array can be decoded before ordinary validation. The useful mechanism is value-preserving representation normalization, without a corrective model request. SuperCli keeps its stricter unique-object and competing-field guards; no neighboring implementation was copied.

## Verification and local cost

Production Registry.Execute fixture:

- Before: singleton scalar wrapper returns ErrInvalidToolArgs, zero handler calls.
- After: one handler call returns a Result equal to the canonical three-range read byte-for-byte.
- Requested lines, ordering and untouched argument bytes are preserved.
- The scalar stays identical through canonical request decoding, including Windows paths, spaces, Unicode, quotes and delimiters; repair is idempotent.
- Native read_many and invoke_tool envelopes each produce one successful model result and one UI result.
- Duplicate/extra/unknown/nonstring/blank cases reject before dispatch.

Three 150ms synthetic registry benchmark runs were executed sequentially on Windows amd64, Ryzen 7 5800X3D. Baseline uses the original 0cc12b9 repair through an overlay; current uses the final type-gated helper. Median:

| Path | Baseline ns/op | Current ns/op | Baseline B/op | Current B/op | Baseline allocations | Current allocations |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Valid canonical scalar | 1,100 | 1,091 | 1,328 | 1,328 | 13 | 13 |
| Already supported singleton list | 7,465 | 7,630 | 7,032 | 7,032 | 100 | 100 |

Both existing accepted paths retain their allocation counts and bytes. Timing variation does not establish a regression or acceleration. An earlier scalar prototype measured about 1.4 microseconds and 1.3 KiB more local allocation for wrapper success versus baseline rejection; those figures precede the final type gate and are not a measurement of live-provider latency or whole-task turn reduction. Exact token savings were not measured.

Passed full internal/tools/files package tests (2.228s), related agent read/invoke tests (0.208s) and go vet for both packages after the final type-gate change. No provider/GPU request, stage, commit or executable build was performed by this worker. Ignored aggregate evidence and baseline/candidate overlays remain under .tmp/session-redundancy-audit-2026-10-02.
