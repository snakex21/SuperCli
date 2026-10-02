# Goal evaluation: compact tool-discovery schemas

Date: 2026-10-02. Audit baseline: main `d752fda`.

## Change

Small successful `tool_search` results now provide an equivalent `Result.ModelText` representation: a valid JSON schema is an embedded JSON value rather than JSON encoded inside a string. All matches, names, signatures, scores, query, hint and schema fields remain. Raw JSON preserves numeric spellings, required fields, types, enums, defaults, nested contracts and description values.

The public `Result.Text` still uses schema strings and is unchanged. Empty and malformed schema text retains its original string representation. No-match responses, failures and outputs over the existing 8 KiB inline limit use the original path. The existing OutputStore guards still reject alternative views for failures, retained evidence or structured previews. Registry schemas and provider tool definitions are untouched.

There is no new permanent prompt, model request, service, dependency or special Zen-provider change. This reduces bytes, not the number of agent turns.

## Local harness comparison and reader audit

The nearby Codex handler `codex-main/codex-rs/core/src/tools/handlers/tool_search.rs` returns typed `ToolSearchOutput` containing `LoadableToolSpec` values (lines 146–176). This demonstrates keeping contracts structured rather than double-encoding them; no implementation code was copied.

The production SuperCli reader audit found no schema-string parser consuming saved discovery history. Search-history retrieval, context projections/pruning and the transcript UI treat tool content as text. Schema-string parsers in discovery tests consume the unchanged public `Result.Text`. Discovery activation and dispatch use registered contracts, not a parsed historical response.

## Saved-session measurement

Read-only SQLite inspection covered the eight latest available sessions with at least 40 messages. Private session IDs, prompts, arguments, project paths and memory contents were neither exported nor included here.

There were 20 native `tool_search` results in this sample. Seventeen complete parsable replies were eligible under the 8 KiB bound; three older previews could not be transformed. The 17 replies contained 42 schemas:

| Complete discovery results | Before | Structured schema view | Saved |
| --- | ---: | ---: | ---: |
| UTF-8 payload bytes | 39,321 | 33,045 | 6,276 (16.0%) |

This is a retrospective JSON-representation comparison, not an exact model-token count. It does not count replays in later requests. The newest substantial session had no `tool_search` calls, so this change would not accelerate that particular transcript. Its input was approximately 93.4% cached; a high aggregate input count alone is not evidence of repeated uncached work or unnecessary loops.

## Deterministic transport verification

`TestToolSearchModelTextWire` runs the real OpenAI-compatible HTTP serializer and production agent loop against a local scripted SSE fixture. Baseline clears ModelText; the changed run uses it. Both discover a tool, dispatch a nested contract with exact fixture values, and finish. Native discovery and `invoke_tool` envelope discovery are covered.

| Discovery form | Discovery result bytes | Cumulative HTTP request bytes | Requests | Target executions | Fixture evidence |
| --- | ---: | ---: | ---: | ---: | --- |
| Native baseline → changed | 810 → 742 | 7,976 → 7,704 | 3 → 3 | 1 → 1 | Preserved |
| Envelope baseline → changed | 810 → 742 | 10,536 → 10,264 | 3 → 3 | 1 → 1 | Preserved |

Each fixture saves 272 serialized request bytes. Protocol/dispatch quality remains equal. These fixtures establish representation and routing correctness; they do not establish a latency reduction or general live-model quality improvement. No Qwen/GPU request or remote provider was used.

## Validation

Passed:

- Complete discovery contract equality, exact numeric spelling, Unicode/spacing, empty and boolean schemas, malformed-schema fallback, large-result retention, errors and no-match responses.
- Real HTTP wire tests above for native and envelope calls.
- Full `internal/tools/core` and `internal/tools/search` package tests.
- Related agent discovery, invoke, discovery persistence and named-discovery tests.

No stage, commit or executable build was performed by this worker. Aggregate audit data and test completion output are in the ignored `.tmp/goal-search-schema-2026-10-02` folder.
