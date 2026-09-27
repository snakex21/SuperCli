# Avoid implementation hints for embedded words and direct negation — 2026-09-26

## Reproduced problem

The automatic implementation-verification hint used substring searches. It recognized fix inside prefix, edit inside credit, and implement inside unimplemented. It also treated direct prohibitions such as Do not edit files and Nie zmieniaj plików as requests to perform changes. Each false positive appended the existing 691-byte contract plus two separator bytes to the user message on the coordinator route.

The earlier read-only-worker fix protects those roles regardless of task wording. This change also corrects the common hint classifier used by ordinary coordinator and implementation-worker loops. Both issues were verified using captured provider requests; the prefix/credit examples are regression fixtures, not claims about previously observed user commands. The saved review prompts used in the prior batch supply real examples of directly negated edit wording.

## Change

Action fragments now require a word start, respecting Unicode letters, digits, combining marks and identifier underscores. Directly preceding negative words in Polish or English suppress that occurrence. Search continues after rejected occurrences, so Do not edit tests; edit production code still receives the implementation hint. Sentence punctuation remains significant: I think not. Fix the bug stays actionable. Explicit rebuild and reimplement entries preserve the legitimate verbs previously recognized through embedded fragments.

This is a narrow local heuristic, not full language understanding. Existing Polish stem matching remains; complex quotation and negation scope are not fully parsed. The hint only adds guidance: neither this classifier nor the role opt-out changes routing, tool availability, permissions, actual user text, or the model's ability to perform a requested task. The instruction text and special OpenCode Zen handling are unchanged.

## Verification

Before the fix, twelve non-action/prohibition fixtures and six coordinator request-capture cases incorrectly included the contract. After the fix:

- All twelve omit it.
- Twelve explicit-action, mixed-clause, sentence-boundary and Polish-inflection cases retain it.
- Native and thin coordinator requests preserve the original non-action text verbatim, use exactly one scripted provider request, and include zero unwanted implementation-contract bytes.
- The read-only-worker suite still exercises independent role policy using review requests that contain an unnegated proposed-fix reference. Implementing roles use explicit implementation prompts. Task continuation and saved delegation replays pass.
- Full go test ./... with saved review prompts, go vet ./..., and CLI/GUI builds pass.

The proven context saving is 693 bytes per falsely classified message, repeated while that message remains in the provider view. No live provider latency or model-behavior improvement is claimed. No additional model/tool calls or permanent prompt text are introduced; classification runs once when preparing the incoming turn.

Artifacts are under .tmp/verification-hint-intent-2026-09-26/: red.json, green.json, suite.json, vet.json, build-cli.json, build-gui.json, and pre-edit source snapshots.

## Installation

CLI --help smoke passed. Both portable executables were installed and their SHA-256 hashes verified against the builds. installed.json records backups and hashes; existing processes were not terminated. Restarting CLI/GUI loads the update. The broader efficiency goal remains active.
