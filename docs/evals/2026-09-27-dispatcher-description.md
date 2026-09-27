# Dispatcher description alignment — 2026-09-27

## Confirmed mismatch and change

The invoke_tool description told the model that every complex or mutating target required tool_search activation. resolveInvokeToolCall already permits visible core tools immediately, as well as active targets and eligible simple read-only targets. Historical synthetic Muse traces contained discovery calls for already-visible patch_file and ctx_execute.

Replace that misleading description with the existing access rules and argument forms. The fixed prefix shrinks from 287 to 228 UTF-8 bytes (59 bytes). The eligible-tool suffix, schema, dispatch implementation, validation, permissions and Zen request serializer are unchanged. The implementation comment is aligned too.

This is a correctness and prompt-size cleanup. The live comparison below does **not** establish saved model calls, lower total input, or a general latency improvement.

## Fixed-history comparison

The opt-in worker continuation evaluator replays the saved successful initial stage locally, then sends a new empty-key handling requirement to the same worker against a synthetic Go project. Both variants use the production native-first thin protocol and an effective 65,536-token window. Only the invoke_tool description is overridden for the legacy arm. All initial-stage assistant messages come from the same seed per model.

Captured first follow-up requests have identical tool names and schemas; the only changed tool field is this description. Muse message bodies are identical. Qwen message bodies differ only in the existing current-date/time system message (16:42 vs 16:44); that difference was not normalized. The local request estimator decreases by 18 tokens for both models.

| Model | Description | Follow-up calls | Tools | Input tokens | Worker wall time | Correct |
|---|---|---:|---:|---:|---:|---|
| Muse | legacy | 7 | 6 | 64,006 | 23.797 s | yes |
| Muse | corrected | 7 | 6 | 64,521 | 21.867 s | yes |
| Qwen | legacy | 4 | 3 | 27,739 | 102.311 s | yes |
| Qwen | corrected | 4 | 3 | 27,750 | 64.005 s | yes |

All four trials pass independent tests, leave supplied tests unchanged, produce a final report, and have zero tool failures or command-gate rejections. Neither arm calls tool_search in this pair. Muse still repeats source inspection and test execution after a successful check. Qwen follows read, patch, test in both arms. Output variation, uncontrolled cloud sampling, order/cache effects and the single local pair prevent attributing the timing difference to this text change.

Artifacts: .tmp/dispatcher-description-2026-09-27, including request-comparison.json, four result.json files and execution records. Full definitions are now recorded by this opt-in evaluator to verify description-only comparisons; no production tracing is introduced.

## Empty native call investigation

A separate legacy-prompt replay in .tmp/worker-empty-call-trace-2026-09-27 captures response-body SSE only (no request/auth headers). It finishes correctly in 12 follow-up calls and 11 tool calls. All 10 native calls have consistent complete arguments across deltas, arguments.done and output_item.done; the other tool call is a textual sentinel. After the existing invoke_tool-to-target history normalization, all native arguments match recorded history.

The previously observed empty calls were not reproduced. These results do not justify changing the Responses parser, and do not prove the earlier problem is fixed.

## Validation and delivery

Existing invoke eligibility, mutation activation, hidden/restricted target, validation, cancellation and wire-evidence tests pass. Full go test ./... and go vet ./... pass. CLI and Windows GUI builds pass, CLI --help smoke passes, and both executable files were installed in the portable application directory with backups and verified hashes.

Measurement scope clarification: these live thin fixtures used the supported tail catalog placement. GUI/TUI defaults already hoist a stable catalog; see [production-profile follow-up](2026-09-27-worker-production-profile.md) for the corrected evaluator and live checks. Within-variant comparisons above remain scoped to their recorded placement.
