# Command capture evaluation — 2026-09-23

Local LM Studio reported qwen3.8-27b-uncensored, Q4_K_M, loaded context
100608. The test uses SuperCLI's production provider, loop, ctx_execute and
read_output with temperature 0, seed 20260923 and max_tokens 2048.
Each trial has a six-model-turn / four-minute evaluation limit, not a new
production loop rule. No live API keys or cloud requests are used.

## Method

A real subprocess emits an early diagnostic to both streams, 10000 later
status lines, a final status and exit code 7. The model receives a task to run
the command and identify the file, line and message. It is not given the answer
or a prescribed tool sequence.

The legacy arm strips the retained capture and rebuilds the error summary
from preview JSON, reproducing the old tail-only evidence. The new arm retains
the capture and includes a bounded head/tail error summary. This compares
evidence availability and presentation, not old versus new process I/O speed.
The final four trials use the same precompiled helper path, prompt and schemas.
Thin runs legacy then new; native runs new then legacy.

## Final scenario: full compiler diagnostic

Diagnostic: source.go:731:19: undefined: missingSymbol.

| Route | Version | Correct final diagnosis | Model calls | Command calls | read_output calls | Seconds | Input tokens | Output tokens |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Thin/sentinel | Legacy | No; evaluation limit | 6 | 1 | 7 | 57.19 | 20739 | 824 |
| Thin/sentinel | New | Yes | 6 | 1 | 6 | 62.85 | 16124 | 1224 |
| Native | New | Yes | 4 | 1 | 3 | 45.42 | 8836 | 899 |
| Native | Legacy | No; evaluation limit | 6 | 1 | 9 | 67.96 | 20012 | 954 |

Both new-version runs identified the exact diagnostic; both legacy runs could
not recover the discarded text. No trial reran the original command. The new
version still made redundant searches after seeing the error. In thin mode it
was slower than the capped unsuccessful legacy attempt; there is no general
speedup claim. These are single observations per route/version, not a statistical
benchmark; cache state, synthetic output and model behavior limit generalization.
No cloud model was called; native compatibility also has deterministic tests.

## Pilots kept in the results

The initial abbreviated diagnostic was source.go:731 missingSymbol, without
the compiler's undefined message. All three thin-mode trials hit six turns
without a final diagnosis:

| Variant | Evidence reached model | Seconds | read_output calls |
| --- | --- | ---: | ---: |
| Legacy tail only | No | 59.85 | 7 |
| Retention only | Yes, by search | 93.76 | 9 |
| Retention + head/tail | Yes, inline and by search | 74.47 | 10 |

The fixture was then made more representative by using a full compiler message,
and both arms were rerun. The pilots show that evidence retention alone does
not guarantee the model will stop searching.

## Reproduction

Build a stable test binary with go test -c -o .tmp/evals/command-capture/agent.test.exe
./internal/agent. Run TestCommandCaptureAB_Live with SUPERCLI_CAPTURE_URL,
SUPERCLI_CAPTURE_MODEL, SUPERCLI_CAPTURE_ARM=legacy or retained, and
SUPERCLI_CAPTURE_ROUTE=thin or native. The test logs CAPTURE_AB_RESULT JSON,
including failures. It skips unless explicitly configured. Ordinary test runs
require no model. Raw results from this session, including tool arguments and
answers, are stored locally in ../../.tmp/command-capture-live-1790169853547.json.
