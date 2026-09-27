# Preserve punctuation in dispatcher text arguments

## Evidence and scope

Code inspection found that invoke_tool's legacy key:value text decoder split at every comma, semicolon and newline. A valid include value such as *.{go,ts}, a regex quantifier such as cache{1,3}, or a Windows filename containing punctuation was rejected or split into incorrect fields before reaching the target. Repeated text keys silently overwrote the preceding value.

This is a deterministic parser regression, not an occurrence counted in the saved GunMayhem session. The heavy saved workers omitted share_context, so their input cost cannot be attributed to full parent-history seeding. Several repeated reads in that snapshot had previously truncated or rejected results; they are not evidence for blindly suppressing rereads.

## Change

The text-only parser recognizes comma/semicolon field boundaries before a following key:value pair and preserves punctuation inside bracketed or quoted content. Newline fields, CRLF, compact comma syntax, literal quotes and backslashes remain supported. Escaped brackets do not hide the next delimiter. Duplicate text keys fail explicitly instead of silently selecting the last value.

Native JSON objects and JSON-object strings use the existing path. Target activation, schema checking, normal execution and verification remain unchanged. No tool schema, permanent instruction, provider request or OpenCode Zen routing change was added. Parsing still uses the existing request in memory and adds no file reads, background work or model calls.

As with the legacy shorthand, unquoted punctuation immediately followed by a field-like key is a separator. JSON object arguments remain the unambiguous representation for arbitrary content.

## Verification

- Baseline Go overlay reproduces rejected glob/regex/path requests and silent duplicate overwrite.
- Native and thin scripted-loop regression: the old parser executes the target zero times; the fixed parser executes it once and puts the result in the next request. Both scripts have two model requests, so this is not a measured reduction in live model turns.
- Literal quoted values, CRLF, escaped regex brackets, character classes, compact separators, duplicate keys, unknown target fields and inactive mutations are covered.
- Full go test ./..., go vet ./..., CLI/GUI builds and CLI --help smoke pass.
- Artifacts: .tmp/invoke-text-punctuation-2026-09-27/ (baseline overlay, red/green results, full validation, builds and installation manifest).

No live latency improvement or provider token saving is claimed. The fix removes a confirmed harness rejection that could otherwise require a corrected call.
