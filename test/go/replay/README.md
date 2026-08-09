# Replay harness — deterministic loop-contract tests

Purpose: protect the rule **"we squeeze performance, but never make the
model worse"**. Every future prompt/tool-protocol change must be
verifiable WITHOUT a live model host: `go test ./test/...` replays
recorded model responses through the real agent loop and asserts the
loop-level contract (what reached the provider, what was executed, how
many turns, what landed in history).

## How it works

- `replay_test.go` defines `replayProvider`, an `llm.Provider` that
  streams RECORDED deltas — content fragments, native tool calls,
  sentinel/XML tool-call text, errors, usage — one recorded step per
  `Complete` call. Fully deterministic, zero network. The provider is
  strict: a loop that calls the provider more times than the recording
  has steps fails the test (turn-count regressions are caught, not
  absorbed).
- Each scenario is ONE json file in this directory (format below), so
  new cases can be added by hand and — later — generated from live
  session recordings.
- Tools are defined in Go (in the matching `TestReplay_*` function),
  because tool behaviour is code, not data. The recording only carries
  what the MODEL said.

## Recording format (one file = one scenario)

```json
{
  "name": "short_id",
  "description": "what this scenario pins",
  "steps": [
    {
      "note": "optional human note for this model turn",
      "deltas": [
        {"role": "assistant"},
        {"content": "streamed text fragment"},
        {"tool_call": {"id": "call_1", "name": "echo", "arguments": "{\"msg\":\"hi\"}"}},
        {"finish_reason": "tool_calls", "usage": {"input": 100, "output": 12, "total": 112}}
      ]
    }
  ]
}
```

Delta fields mirror `llm.Delta`: `role`, `content`, `tool_call`
(`id`/`name`/`arguments`), `finish_reason`, `usage`
(`input`/`output`/`total`/`cached_input`/`reasoning`), `err` (string —
replayed as a stream error), `notice`. Sentinel («name\nkey: value»)
and XML (`<tool_call>...`) calls are recorded as plain `content`
fragments — deliberately split across deltas where the scenario wants
to pin the streaming scanner.

## Scenarios

| file | pins |
|---|---|
| `tool_call_sentinel.json` | sentinel tool call split across stream deltas is parsed, executed, and its result returns to the model |
| `command_failed_diag.json` | structured `command_failed exit=…` diagnostics reach the model; the recorded next step cites the exit code |
| `http_failed_body.json` | `http_failed status=… body:…` (error body included) reaches the model; next step acts on the body |
| `multi_tool_batch.json` | several tool calls in one model turn all execute; results append in call order |
| `preflight_addon.json` | preflight repo block rides the user message (never system), is one-shot, and does not disturb the tool round-trip |
| `prune_mid_session.json` | mid-session prune keeps history coherent: markers in place, pairing intact, protected tail kept, next request appends to a stable prefix |
| `truncated_result.json` | a HeadTail-truncated tool result keeps its `[... omitted_bytes=N ...]` marker all the way to the provider |

Noop-gate is app-level and zero-LLM by construction (a gated batch run
makes no provider call at all), so it has nothing to replay — its
behaviour is pinned by `internal/app/noopgate_test.go` and
`internal/app/defaults_test.go`.

## TODO — live-eval skeleton (deliberately not built yet)

A future `-tags eval` suite of 10–15 REAL tasks (edit a file, count
matches, multi-step repo work) run against a live local host, with the
same metrics as here (success, turns, failed tool calls, tokens, tool
wall time) written to a baseline file for A/B before/after
prompt-protocol changes. Recordings captured from those runs should be
convertible into new replay scenarios in this directory.
