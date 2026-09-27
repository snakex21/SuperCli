# Do not add implementation instructions to read-only workers — 2026-09-26

## Confirmed cause

The saved GunMayhem review delegations at sequences 949 and 1371 explicitly ask for read-only analysis and say "Do not edit files or commit" / "Do not edit or commit". The existing mutation-hint classifier matches the substring "edit ", including this prohibition. The coordinator route therefore appended its 691-byte implementation contract plus two separator bytes to the review worker's user message. That contract tells the worker to make the requested change, run a concrete check, and continue until implementation is complete, despite its read-only role and restricted tools.

The same injection recurred when send_message continued such a worker. Older injected copies remained in the in-memory context. This is a concrete contradictory-context defect; it is not proof that it caused the long duration or all the shell attempts visible in the historical reviews.

## Change

Read-only built-in roles (review, plan, explore, advisor) now explicitly opt out of the automatic implementation hint through SubAgent / LoopConfig metadata. AgentTool passes that policy into the new loop, and the same loop retains it on continuation. The advise flag still selects the advisor role and inherits its policy. The setting follows the actual spec, not its name, and a copied custom read-only role retains it.

Default coordinator, code, general, and ordinary custom-role behavior remains unchanged. The internal opt-out is not a permission switch: tool allowlists still enforce what each role can execute. No system prompt, schema, model endpoint, special OpenCode Zen route or storage location changes. Shared historical messages are not rewritten by this change.

Each affected current user turn avoids 693 bytes of contradictory boilerplate in every subsequent request that includes that message. Multiple continuations no longer accumulate new copies. This is a byte-level payload result, not an exact tokenizer or provider-cache measurement. No extra model or tool call is introduced.

## Verification

A deterministic provider captures the requests made by the real AgentTool and SendMessageTool. Coverage includes both native and thin protocols, all built-in roles, the forced-advisor flag, a default custom implementation role, and a copied custom read-only role. Each probe delegates once and continues the same worker once; exactly two provider requests occur. Read-only prompts remain verbatim, and patch_file / ctx_execute remain unavailable. Implementing roles keep the contract.

The exact two saved review prompts are replayed locally via SUPERCLI_READONLY_PROMPTS using .tmp/readonly-worker-hint-2026-09-26/saved-prompts.json. Before the fix, all read-only probes received the extra 693 bytes. After the fix, the injected bytes are zero on both initial and resumed runs. No private prompt was sent to an external model; these are captured-request tests.

Artifacts: red.json, green.json, saved-prompts.json, suite.json, vet.json, build-cli.json, build-gui.json in the same app-local artifact directory. Full go test ./... (including the two saved prompts), go vet ./..., and CLI/GUI builds pass. Live provider latency and changes in real model behavior were not measured in this batch.

## Installation

CLI --help smoke passed. Both portable executables were installed and verified against the build hashes. installed.json records hashes and backups; existing processes were not terminated. Restarting CLI/GUI uses the update. The broader efficiency goal remains active.
