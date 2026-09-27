# Initial worker tool visibility — 2026-09-27

## Evidence and question

The saved Muse runs in `.tmp/worker-hint-ab-2026-09-27/muse-1-without/deferred.json` and `muse-2-with/deferred.json` call `tool_search` for "patch file edit" / "patch file editing" after receiving source. This adds a model round before editing. Does the current worker actually receive `patch_file` in its initial request?

## Verification

`TestCodeWorkerInitialRequestIncludesPatchSchema` creates a real built-in code worker, uses the normal loop and provider request encoders, and captures the first HTTP body on a local test server. It covers Chat Completions with the Qwen model ID and Responses with the Muse model ID, each with thin mode off/on and the stable toolset enabled.

The Zen test retains the real Zen branch (gate tools, endpoint and session/client headers) while rerouting HTTP to localhost. No external model is called and no inference-performance result is implied.

| Transport | Thin | Tool definitions | Patch properties |
| --- | --- | ---: | ---: |
| Chat Completions | off | 6 | 6 |
| Chat Completions | on | 7 | 6 |
| Zen Responses | off | 8 | 6 |
| Zen Responses | on | 9 | 6 |

In every case the first body includes `patch_file` with its description and the six parameters (`path`, `old`, `new`, `changes`, `expected_count`, `base_hash`). It also includes the existing discovery description saying to use already available tools directly. The synthetic worker completes with one HTTP request; no discovery has run beforehand. Counts are for this restricted fixture, not a full installation's registry.

## Decision

The missing-initial-schema explanation is contradicted by the current request path. The live Muse repetition remains an observed model choice; this test does not establish why it chose discovery or prove that a different description would improve it. Do not add pre-discovery, provider calls or another instruction based on this hypothesis. Keep the existing production behavior and add the HTTP regression coverage.

This does not resolve all repeated reads/checks or assert that model behavior is optimal. The separate completion-hint removal experiment also failed to justify a production change.

## Validation and artifacts

- Focused four-case HTTP test: passed.
- `go test -timeout=90s ./internal/agent ./internal/llm`: passed.
- `go vet ./internal/agent ./internal/llm`: passed.
- `git diff --check`: passed.
- Artifacts: `.tmp/worker-initial-request-2026-09-27/` (`initial-request.json`, `tests.json`, `vet.json`, `diff-check.json`).

Only tests and this evidence record changed. The existing verified CLI/GUI builds from the whitespace-patch batch remain installed; a test-only audit requires no replacement binaries. No production instruction, tool schema, Zen transport or data path changed.
