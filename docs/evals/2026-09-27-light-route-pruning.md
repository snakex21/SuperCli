# Pruning respects optional light routes — 2026-09-27

## Evidence

The optional chat/advisor/clarify routes omit historical tool exchanges and use
a bounded, sticky dialogue window. The ordinary coordinator is the default when
the navigator is disabled; this change does not enable navigation or add a mode.

Pruning still used the full coordinator-style history projection on a light
route. Three initial tests reported a 7944-token reclaim with an unchanged
request estimate: 1680 for chat, 1683 for advisor/clarify. Old project tool
results were rewritten even though those results were not sent on that route.

Six active-turn cases also showed inflated savings and needless mutation of
older project work: the fixture reported 47356 while only 7412 estimated tokens
actually disappeared from the light request. Hidden history did not prevent
the defect.

Evidence: .tmp/light-route-pruning-2026-09-27/red.json.

## Change

A pure chatHistoryProjection helper now selects the exact same bounded dialogue
and current turn for request assembly, estimation and pruning. Only actual
request assembly updates the sticky window start. The existing eligibility
rules, four-message jumps, current-turn tool pairs, system prompts and routing
settings are preserved.

Pruning uses optional source-index mapping through:
- hidden ranges and their displayed placeholders;
- native-reasoning projection;
- omission of archived completed tool exchanges;
- the light-route history selection.

Its proposed-after view uses the same selection before accepting a cache-prefix
rewrite. Coordinator pruning continues through its already shared projection.

The duplicated light-history selection loops in request assembly and estimation
are removed. Normal requests do not allocate source-index maps. The helper sizes
its result for eligible history instead of reserving space for every old tool
message. No extra model requests, prompt instructions, schemas, source reads,
provider transports or data locations are introduced.

## Validation

- Initial no-gain reproductions now leave all canonical messages unchanged and
  emit no pruning event.
- Twelve active-turn combinations pass: three routes × hidden history yes/no ×
  retrievable archive yes/no.
- The fixture first crosses the sticky-window threshold. Estimation and pruning
  leave the window position unchanged; request assembly owns its advance.
- Raw request estimates match prepared requests before pruning.
- Accepted gain matches the actual request reduction. Older project history and
  the latest call/result pair remain unchanged.
- Existing growing-window, request-estimate, mixed-batch, pruning, archive and
  compaction tests pass.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds and CLI --help pass.
- Both installed executable hashes match their builds. Backups and test output
  remain inside .tmp/light-route-pruning-2026-09-27.

Installed:
- supercli.exe: e2974e2e00807b557ea20bf436841b41e594a10c6b0028b75074d6d0dd5a7328
- supercli-web.exe: 921e2e6a89a0a939366b975b8b34a54cb25c275e2475691450f07fad9454f2ef

The numbers describe deterministic local estimates, not live provider billing
or wall-clock acceleration. This fixes optional light-route pruning; model
summary compaction on those routes is a separate audit. The ordinary coding
route, OpenCode Zen transport, and portable storage policy are preserved.
