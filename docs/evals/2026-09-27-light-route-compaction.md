# Light-route compaction uses the actual request window — 2026-09-27

## Confirmed defects

Optional light-route pruning had been corrected, but summary compaction still
priced the coordinator history and selected cuts from older project messages.

Eighteen initial cases failed:
- Manual compaction grew a chat request estimate from 248 to 706 tokens;
  advisor/clarify grew from 251 to 709. Excluded project tool results were
  incorrectly counted as savings.
- Automatic, context-limit and model-switch entry points made the same
  ineffective replacement when fixed briefing overhead triggered the check.
- Two short outgoing turns with a large archived result paid one unnecessary
  summary call.
- Large recent project output, omitted on the light route, forced compaction of
  the previous correction and active call/result pair.

Evidence: .tmp/light-route-compaction-2026-09-27/red.json.

## Change

Compaction now uses the existing exact light-route request selection for token
cost and user-turn boundaries. Its indices are mapped back through visibility
to the full evidence view. Visibility placeholders do not count as real user
instructions.

The summarizer still receives the complete visible evidence prefix, including
tool observations needed for a later return to project work. Only cost/boundary
selection is based on the current request.

Both ordinary automatic compaction and model handoff use the mapped request
boundary. A cut at the beginning of the outgoing conversation means there is no
older prefix to compact, even when the archive contains many earlier turns.

The coordinator path, routing settings, prompt text, model call protocol,
OpenCode Zen transport and portable storage locations are unchanged.

## Verification

- All eighteen initial cases pass. Ineffective summaries are rejected; two
  short outgoing turns make zero summary calls.
- Twenty-four route/entry-point/hidden-history combinations verify that older
  project history outside the sticky chat window causes no summary call,
  canonical rewrite, visibility change or window movement.
- Switching those fixtures back to the coordinator still exposes the earlier
  project evidence.
- Three hidden-prefix cases verify useful compaction, original-index mapping,
  summary input, preservation of the recent correction and active pair, and
  reduced request estimates.
- Existing compaction, reasoning, archive, pruning and growing-window tests pass.
- Full go test -timeout=90s ./..., go vet ./..., CLI and GUI builds pass.
- CLI --help passes; installed hashes match the new builds. Backups and
  verification artifacts remain in the application directory.

Installed:
- supercli.exe: c5a1d2dbbdefe0d42b07bd4385467372befd52a7d6da9771ababe0aa163db4b0
- supercli-web.exe: fb28f5252c3e61957d71b4cdde62a3e63d3a31c3d1c3c36f8d8ed2729b3553a6

One unnecessary summary call is avoided in the reproduced short-history
scenario. Other ineffective-summary cases retain the single existing attempt
and reject its expansion. Token counts are deterministic local estimates, not
live provider billing or a measured whole-session latency improvement.
