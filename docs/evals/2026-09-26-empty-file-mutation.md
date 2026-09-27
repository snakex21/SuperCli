# Correctly completed empty-file mutations — 2026-09-26

## Reproduced fault

The built-in file tools correctly created an empty marker file or removed all content from an existing file. The common post-execution verifier then changed that success into an error solely because the file size was zero. The loop recorded a failed mutation and blocked goal completion even though the requested bytes were already on disk.

Reproduction uses the real registry, tools and agent invocation path, not a simulated tool success. All five cases failed before the fix:
- create_file creates an empty .gitkeep;
- patch_file removes all content;
- a deletion matches CRLF content through the existing line-ending normalization;
- two deletions remove both blocks;
- a replacement followed by removal of that replacement leaves an empty file.

The existing write_file path already allowed an explicitly empty payload. The newly fixed cases are create_file and patch_file. This defect was found by source review and reproduced in controlled fixtures; it is not claimed to explain a particular historical GunMayhem failure.

## Change

The patch engine retains whether its computed final content is empty, including multi-change patches. The file tools carry this fact internally as Result.EmptyFileExpected. The verifier still stats the output path but accepts zero bytes when the tool's intended result was empty.

The flag is derived from computed content, not a model claim or a string parsed from success prose. It is excluded from JSON and model-visible output. No new tool parameter, system instruction, model call or second file read was added. Existing-file refusal, stale-hash guards and patch anchor validation are unchanged.

Missing output files, unexpected empty results, and explicit content checks that are not met still fail. The compatibility rule for explicitly empty write_file arguments is preserved.

## Validation

- Red/green regression in internal/agent/empty_file_mutation_test.go.
- The real on-disk file is checked to exist and contain zero bytes.
- Successful empty mutations do not increment identical-failure counters or set the concrete-failure flag.
- The existing goal completion action succeeds immediately after the mutation, with no repair edit.
- Internal metadata does not appear in serialized tool results or model content.
- Missing files, unexpected truncation and unsatisfied expected-content checks remain failures.
- Related create/patch/verifier tests, full go test -timeout=90s ./... and go vet ./... pass.

Code: internal/tools/core/registry.go, internal/tools/core/verifier.go, internal/tools/fileops/patch_file.go, internal/tools/files/create_file.go and internal/tools/files/patch_file.go.

Local evidence: .tmp/empty-file-mutation-2026-09-26/before.json, after.json, constraints.json, suite.json and vet.json.

This removes a deterministic false failure and its completion blockage. It does not quantify the frequency of these operations in normal work or promise an average provider-latency saving.

## Installed build

Both Windows binaries were rebuilt and installed after the passing checks. CLI --help passed. Backups and verified hashes are stored under .tmp/empty-file-mutation-2026-09-26/before/ and installed.json. Restart running instances to load the fix.
