# ZIP extraction dependencies

Baseline: `b754435` (dev10). This is a correctness repair, not a measured reduction in model turns or tokens.

`read_zip` can list an archive or extract entries to disk. The batch scheduler previously gave both actions only the archive's read footprint. Extraction followed by `read_lines` or `patch_file` of a destination file could therefore run in one parallel wave. A read could return stale/missing contents; a patch could fail before the archive had created the text it was meant to replace.

The scheduler now keeps listing/default-action calls as archive reads. An extraction with an explicit nonblank `target_dir` reads the archive and writes that directory, using the existing path resolution and directory-overlap rules. Missing, blank, wrong-type, or otherwise unknown destinations retain the existing sequential fallback: the default target depends on the tool instance's `ExtractRoot` and a timestamp, so the scheduler does not guess it. Invalid/unknown actions also retain the barrier. `read_zip` remains a mutating tool; its schema, validation, invocation results, and provider paths are unchanged.

## Matched reproduction

The ignored fixture and overlays under `.tmp/goal-harness-round8-2026-10-02` used real registered ZIP, line-read, and patch tools through `invokeToolCalls`. A controlled completion gate held extraction until a concurrently dispatched dependent had finished, or a single 20 ms test wait elapsed. This controls interleaving; it is not a speed benchmark or progress polling.

| Case | Baseline | Candidate |
| --- | --- | --- |
| Explicit relative/absolute destination + read | One unsafe wave; dependent failed before extraction | Two ordered waves; extracted evidence returned |
| Explicit destination + patch of archive text | One false patch failure; final edited contents wrong | Patch succeeded; final contents correct |
| Omitted destination with custom portable `ExtractRoot` | Unknown destination incorrectly admitted to one wave | Sequential barrier; actual default extraction succeeded |
| ZIP listing / unrelated explicit extraction destination | Existing independent scheduling | Independent scheduling preserved |

The baseline fixture exited with failure; the same fixture and candidate overlay passed. Durable tests additionally cover reversed dependencies, malformed/null action or target, blank targets, exact result order/IDs, and direct native calls versus resolved `invoke_tool` envelopes. All fixture files are created under Go's portable temporary directory; no user archive, model, network endpoint, or program is used.

## Harness comparison and limits

Pi's agent loop serializes a batch when a tool requires sequential execution; Codex uses exclusive admission for tools not approved for parallel execution. SuperCli already has resource-aware waves, so the useful change is to correct the omitted ZIP write footprint rather than add another router or cache.

The six previously selected completed sessions contained no `read_zip`, `read_docx`, `read_pdf`, or `read_xlsx` calls. This repair therefore has a reproduced source/fixture basis, not historical evidence of extra model turns. A separate proposal to mark office readers read-only was rejected as a performance port: the current scheduler already recognizes those readers' paths when a workspace is available. No latency, RAM, token, or turn savings are claimed here.

Source scope: `internal/agent/loop_tool_schedule.go` and `internal/agent/tool_zip_schedule_test.go`. Validation: scoped agent/office tests, scoped vet, and whitespace checks.
