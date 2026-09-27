# Compaction prices outgoing tool evidence — 2026-09-27

## Confirmed defects

Compaction already respected hidden messages and projected native reasoning.
However, ordinary requests additionally omit eligible completed tool exchanges
when the session archive can retrieve them through search_history. The
compaction guard and emergency/manual boundary still priced their full text.

Reproductions through the actual Loop entry points showed:

- Manual compaction accepted a larger summary: estimated request 303 -> 860
  tokens. Automatic, context-limit and model-switch paths made the same
  replacement (55301 -> 55858 with fixed overhead forcing those checks).
- An oversized archived result in the recent completed turn forced manual
  compaction across the previous correction and current tool exchange, although
  the result was absent from the outgoing request.
- Two short visible turns plus a large archived tool result caused an unnecessary
  summary call. After the fix this case makes zero summary calls and leaves
  history unchanged.

Evidence: .tmp/compaction-resolved-tools-2026-09-27/red.json.

Testing the rejected-summary fallback then exposed related existing defects:
HideLastUserTurns counted already hidden instructions and hid leading system
messages. It could lose the last visible correction and standing instructions.
Calling it with keep=0 also panicked. Both provider-error and oversized-summary
fallback cases reproduced the losses; the zero case reproduced the panic.
Evidence: fallback-red.json in the same directory.

## Change

Compaction now has two views: complete eligible evidence for its summarizer,
and the actual resolved-tool projection for boundary selection and savings.
The shared projection optionally returns source indices. It maps the chosen
request boundary back through the evidence view and any hidden-message mapping
before replacing canonical messages.

The full history is projected before taking a prefix. Projecting just a prefix
would incorrectly retain small exchanges that a later completed turn has already
made eligible for omission.

Ordinary requests do not allocate the new index map. Omission rules, retrieval
requirements, recent-evidence budget and active tool protocol are unchanged.
Workers without their own archive and sessions with missing/unreliable storage
still retain and price their required tool evidence. Matching native
continuation state still prevents early tool-history omission.

The fallback counts visible instruction turns, preserves leading system
messages, and handles zero retained turns without indexing past the end.
Previously hidden messages remain hidden.

No model instructions, schemas, provider transports or storage locations changed.
The special OpenCode Zen transport is untouched.

## Verification

New tests cover:
- rejection through manual, automatic, context-limit and model-switch entry points;
- preserved previous correction and current complete tool exchange;
- zero summary calls for two short outgoing turns with large archived results;
- pricing small older exchanges against later replies in the full conversation;
- required evidence with no writer/history tool, outage, pending/lost writes,
  or native continuation state;
- mixed large/small batches, hidden history and correct boundary mapping;
- actual session-store projection plus retention of the full original archive;
- failed/oversized summary fallback preserving policy and the last visible
  correction, and zero-turn clearing.

The manual reproduction is now 303 -> 303; the ineffective summary is rejected.
Handoff likewise leaves the original context unchanged. Automatic/error paths
retain their existing hide fallback, now preserving system instructions.
The evidence needed by the summarizer is retained even when omitted from normal
requests.

Full go test -timeout=90s ./... and go vet ./... pass. Both CLI and GUI builds
pass, CLI --help passes, and installed executable hashes were checked against
the builds. Backups and verification artifacts remain inside the application.

Installed:
- supercli.exe: 01cbf55081d6ecb0fb28ddd76217d1736d1ca8ee4077442e2210a648c5110989
- supercli-web.exe: 4c34bc32e10df97ac8ad9b690c8c53acd94326f0e76ec118cedd991c83111c40

The numeric token results are local estimates on deterministic fixtures, not
provider billing or live latency measurements. One summary call is demonstrably
avoided in the short-history fixture; no general real-session time saving is
claimed. This does not establish that these bugs caused historical long sessions.
