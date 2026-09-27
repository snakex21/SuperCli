# Worker continuation: serialized evidence audit

## Question

The two-stage live replay showed redundant discovery, reads and passing checks in Muse's continuation, despite a correct edit. The earlier initial-request audit did not establish what the endpoint receives after a tool exchange and a subsequent send_message.

## Check and result

TestWorkerContinuationPreservesWireEvidence uses real code-worker creation, a real bounded file read, send_message on the existing worker, actual provider serializers and SSE decoders. Both HTTP transports are redirected to a local scripted server; no external model is contacted.

All 12 cases pass: Chat Completions / Zen Responses, native / thin-tail / thin-hoist tool layouts, and previous-reply reasoning retention on / off.

The requests preserve:

- Exactly one worker loop, one initial read, a final report and a subsequent continuation request.
- Matching call/result IDs and the actual read content, immediately after the read and again on continuation.
- The initial task, final report and follow-up instruction.
- Unchanged tool definitions, including the core coding tools.
- Native continuation state attached to the tool call. The separate completed-reply state follows the user's retention preference.
- The special Zen route and session/client headers.

This is a small, unpruned fixture. It rules out a basic continuation serialization loss in these configurations; it does not prove that longer live histories retain every result, or explain Muse's redundant calls. The live replay's 16k fallback context and provider behavior still need isolation before a production change is justified.

Only this regression test and audit documentation were added. Production behavior, instructions and binaries are unchanged; there is no speedup claim. During fixture development the first draft used incorrect read_lines argument names; the corrected test uses its actual file/from/to schema. That initial test failure was a fixture error, not a SuperCli regression.

Validation: all 12 wire cases, go test -timeout=90s ./internal/agent and go vet ./internal/agent pass.

Artifacts: .tmp/worker-continuation-wire-2026-09-27/
