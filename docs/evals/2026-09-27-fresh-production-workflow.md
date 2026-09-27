# Fresh production-profile task and continuation — 2026-09-27

## Scope

A fresh code worker fixed two interacting defects in a synthetic four-package
Go project. send_message then resumed the same worker to implement a third
behavior while retaining the earlier fixes. Unlike the earlier seeded follow-up
comparison, both stages used live model responses.

Both backends used execution.Resolve defaults: ThinTools=true,
StableToolset=true, CatalogHoist=true, with a 65536-token worker window. Only
synthetic fixture files were sent. The installed production source from the
navigator/media batch was used without further production edits.

## Results

| Backend | Stage | Model calls | Tools | Input tokens | Worker time | Correct |
|---|---|---:|---:|---:|---:|---|
| Qwen 3.8 27B / LM Studio | Initial | 5 | 6 | 21392 | 58.402 s | yes |
| Qwen 3.8 27B / LM Studio | Follow-up | 6 | 5 | 42194 | 91.295 s | yes |
| Muse Spark 1.2 contributor free / Zen | Initial | 8 | 7 | 37167 | 16.922 s | yes |
| Muse Spark 1.2 contributor free / Zen | Follow-up | 4 | 3 | 31496 | 9.884 s | yes |

Both runs created exactly one child Loop. The worker run counter advanced from
one to two. All four outcomes passed independent go test ./..., retained the
supplied tests byte-for-byte, and produced reports consistent with the edits and
verification. No request was pruned in this fixture.

These are observations, not an A/B speedup comparison. Model sampling, response
lengths and cache state differ; Qwen's follow-up also contains the evaluation
defect below.

## What the additional calls did

Qwen's first stage batched listing/search, read source/tests together, patched
both files in one response, ran tests once and reported. On continuation it read
only the new test and used the prior source evidence to patch the implementation.
After passing go test ./..., it requested a verbose focused verification.

Muse first listed the root, then requested a deeper tree, read the four relevant
source/test files together, applied the two patches separately and ran tests.
It subsequently requested -v to obtain individual test names. The continuation
used one batched source/new-test read, one patch and one verbose test command.

The captured provider request views include the earlier successful stdout and
exit_code=0 before the later verification calls. Thus this trace does not
establish a lost-success-result bug. Repeated verification produced more detailed
test output, and no identical failed call loop occurred.

## Evaluation-only false rejection

Qwen requested:

    go test -v -run TestEmptyKeysSkipped|TestPublishCanonicalKeys ./service/

This is an argv vector passed directly to Go, not a shell pipeline. Go accepts
the trailing slash. Running that exact vector independently in the fixture
succeeds. The evaluation allowlist accepted ./service but rejected ./service/,
injecting an error and a corrective model request that production does not
require.

The evaluation allowlist now accepts the two equivalent fixture-package
spellings ./queue/ and ./service/. Other packages, paths escaping the fixture,
tool overrides, shells and edits to supplied test files remain disallowed.
Two regression cases fail before the correction; the focused gate controls and
all agent tests/vet pass after it.

This is a correction to future measurements, not a newly shipped CLI speedup.
The recorded six-call follow-up remains reported unchanged. It must not be used
as a clean production latency baseline. No live rerun was needed to establish
that the rejected command was valid; its exact argv was executed successfully.

## Artifacts and remaining interpretation

Artifacts are under .tmp/current-production-workflow-2026-09-27/:
- qwen/result.json and muse/result.json, full requests, reports and source;
- qwen-run.json and muse-run.json, completed operation results;
- passing-evidence.json, exact passing-result IDs and subsequent requests;
- gate-red-and-command.json, false rejection plus successful actual command;
- agent-tests.json and agent-vet.json.

Only evaluation/test files changed in this batch, so app binaries remain the
fully tested navigator/media builds already installed. The new evidence supports
same-worker continuation and result retention on this task; it does not prove
that every model avoids rereads or repeated checks on every large project.

There is no justification in this trace for suppressing successful test reruns,
adding persistent prompt instructions, or changing the OpenCode Zen transport.
