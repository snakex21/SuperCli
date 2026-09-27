# Deferred code-worker schemas and process access — 2026-09-26

## Problem

The built-in code worker marked every allowed tool as always visible in native-tool mode. Each coding request therefore carried full Word, spreadsheet, PDF and archive schemas, even when the task only needed source reads, edits and commands. Thin workers already used a compact catalog.

The same role could call ctx_execute but lacked process_session in its allowlist. It could not use the existing managed-process start/wait interface for longer checks. General workers already inherited that capability.

## Change

- Add optional DeferredTools to worker roles. The code role defers six document/archive tools and process_session when using native tool definitions.
- Keep these tools registered and executable; expose their schemas through a child-scoped tool_search when needed. Only add this discovery tool if a permitted deferred tool actually exists.
- Keep ordinary source-editing tools immediately visible. Existing Word-task detection still activates read_docx/edit_docx before a request for a document task.
- Preserve thin-worker catalog and dispatch behavior. Add process_session to the code role's permitted tools in both modes.
- Retain read-only roles' execution restrictions and the worker nesting limit. Child discovery cannot activate parent tools or expose tools outside its allowlist.

Selection uses the worker's resolved backend profile, including mixed local/cloud configurations. No system instructions or permanent model calls were added. Provider transport, including the special OpenCode Zen path, is unchanged.

## Measurement

A deterministic fixture uses actual registered tool implementations and serializes the native definitions returned by the worker loop:

| Measurement | Before | After |
| --- | ---: | ---: |
| Visible definitions | 11 | 6 |
| Serialized definition bytes | 17,559 | 5,280 |

The difference is **12,279 JSON bytes** (about 70% of this fixture's tool-definition section). The fixture contains four ordinary code tools, six document/archive tools, process_session and discovery; the loop also supplies its own read_output. Before the change, the code-role allowlist excluded process_session and discovery.

These numbers are not whole-request token counts or measured provider latency. A production worker may have additional ordinary tools. Deferred schemas return when activated, so the saving applies while they are unnecessary. Using a less common deferred tool can require discovery; Word tasks retain their existing automatic activation.

## Verification

- Red/green native-schema regression and byte comparison.
- Discovery activates a real spreadsheet tool in the child only.
- Word tasks have both Word schemas ready in native and thin modes.
- Thin catalog retains all deferred capabilities.
- Forbidden tools and nested delegation stay inaccessible.
- Real process start followed by a blocking wait succeeds through both native dispatch and thin invoke_tool.
- Existing CLI and GUI mixed-backend profile tests pass.
- Full go test -timeout=90s ./... and go vet ./... pass.
- CLI and GUI builds pass.

The first full run caught an unnecessary loader in an otherwise empty worker registry. Discovery now appears only if an allowed deferred tool was actually registered; the existing profile tests pass without weakening their assertions.

Local artifacts: .tmp/worker-schema-2026-09-26/{before,after,regressions,suite-initial,suite,vet,build-cli,build-gui}.json.

CLI --help smoke test passed. Both executables were installed with prior-build backups and matching SHA-256 checks; see .tmp/worker-schema-2026-09-26/installed.json. Running instances require a restart.
