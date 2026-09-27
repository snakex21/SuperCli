# Worker inherits the legacy context-window resolver

## Evidence and scope

A controlled continuation comparison requested windows of 16,384 and 65,536 tokens via LoopConfig.WindowFor. Both workers reported 16,384: AgentTool copied ContextWindowFor and ScopedContextWindowFor, but omitted the still-supported legacy WindowFor callback.

Four regression cases fail before the fix: a legacy-only resolver and fallthrough from an unresolved newer resolver, each with the main provider or a distinct worker provider. The worker silently used the fallback instead of the configured 65,536 / 32,768 tokens.

The current CLI, batch and GUI entry points use the newer callbacks, which were already inherited. This finding is not evidence that their configured windows were ignored, and is not presented as the cause of the user's original repeated-work reports.

## Change

AgentTool now forwards the legacy resolver as well: three added lines, with no changes to precedence, prompts, schemas, model calls or the Zen path. The callback resolves the worker's actual model on each use; it does not copy the coordinator's resolved numeric limit.

Eight regression cases now pass, covering the resolver cascade, separate worker model/connection, and a changed limit on send_message continuation without recreating the loop. Source-aware and scoped overrides keep their precedence.

The live evaluator also asserts the effective requested worker window before any model request, preventing a silently invalid comparison.

## Validation

- Red/green context inheritance regressions.
- Twelve existing continuation wire cases.
- Full go test -timeout=90s ./..., go vet ./..., CLI and GUI builds.
- CLI --help smoke check.

No model-turn or latency saving is claimed for this compatibility fix. Both portable executables were rebuilt from the verified worktree.

Artifacts: .tmp/worker-window-inheritance-2026-09-27/
