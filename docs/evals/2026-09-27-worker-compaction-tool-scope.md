# Worker compaction uses the worker registry — 2026-09-27

## Confirmed defect

AgentTool inherited ParentLoop.summarizer unchanged. The standard summarizer was
a closure over the parent's registry.ActiveNames callback. Therefore a worker
that compacted its conversation appended the coordinator's loaded_tools facts,
not its own activations.

This is misleading in either direction: a read-only worker can be told that a
forbidden parent editor is loaded, and a tool discovered only by the worker can
be omitted. Tool permissions themselves were not bypassed.

The fixed saved-session sample contains six task calls and one send_message.
Reports retain IDs/status/evidence references; historical read-only shell attempts
were already addressed by earlier role/hint fixes. This audit does not establish
that the newly found registry-capture bug caused the historical long runtimes.

## Reproduction

The test uses the real AgentTool and SendMessageTool, a built-in review role and
separate parent/child registries. The parent has edit_docx activated; the child
cannot execute it. Four scripted worker turns and two real compaction operations
run through one retained worker Loop.

All four initial cases (native/thin × child activation present/empty) failed
before the fix. Each child summary said loaded_tools: edit_docx, including when
the worker had no on-demand activation or had activated read_lines/read_many.
The second compaction after send_message still repeated the parent's list.

Evidence: .tmp/worker-compaction-tool-scope-2026-09-27/red.json.

## Change

The loop's common summarizePrefix entry point provides a private per-call scope
containing its own registry callback. The standard summarizer uses that scope;
standalone callers without a loop retain the explicit callback passed at
construction. A scoped empty registry is authoritative and never falls back to
the parent's callback.

Manual compaction now also calls summarizePrefix, so it shares scoping and helper
timing with automatic and model-switch compaction. The configured side-provider
and existing fallback remain unchanged. No instructions, tool schemas, registry
permissions, provider transport or tool activation behavior were changed.

## Verification

- All four real delegation/continuation cases pass; the same worker Loop survives.
- Activations changed between the worker's two compactions are reflected correctly.
- The parent registry is unchanged and forbidden tools remain absent from review.
- Six entry-point/fallback combinations pass: manual, automatic and model-switch,
  each with and without a failed configured side-provider.
- The scoped callback is read after completion, so current worker activations are
  used even when the active provider changes them during the test.
- Explicit empty loop scope and standalone callback compatibility pass.
- Exactly six main requests are made in each delegation fixture: four worker turns
  and two summaries. No extra model request is introduced.
- Full go test -timeout=90s ./..., go vet ./..., CLI and GUI builds pass.

This corrects deterministic context given to a worker. A decrease in redundant
discovery, failed calls or wall-clock task time has not been measured live.
