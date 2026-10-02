# Shorter tool descriptions — 2026-10-02

Five tool descriptions were shortened: ctx_execute, invoke_tool, ask_user, send_screenshot and process_session. The ctx_execute argv/PATH/no-shell explanation moved into the command property; its JSON-string and Windows built-in examples remain. The workdir default remains explicit. The contradictory old claim that the command returned only stdout was removed; the actual bounded JSON result fields remain listed. DOCX routing, question/visual limits and invoke eligibility are preserved.

The screenshot descriptions retain clipboard defaults, automatic preview, opt-in pixel analysis, exact PID/title/ID selection, no screen fallback and the distinction between visible screen, background application window and desktop wallpaper/icons. The desktop enum was added separately with its capture implementation; shortening these descriptions does not add or remove an argument. Process sessions retain direct executable ownership, PTY merged output, short-command routing and capped lifetime/output/concurrency.

## Size and contract checks

On the completed historical 16-tool wire fixture, the final three core descriptions save 308 serialized bytes (13,885→13,577 bytes). No final provider-token reduction is measured for these shorter descriptions. That fixture predates the desktop enum; its absolute 13,885-byte size is not the final production request. The current screenshot ToolDef, after adding desktop but before shortening prose, serializes to 1,757 bytes; after shortening it is 1,491 bytes (266 fewer). The process-session description saves another 54 bytes. Combined description savings are 628 bytes when all five definitions are included. Request projection/escaping and which tools are present affect the actual total.

Recursive comparison excluding only description properties confirms identical JSON Schema types, required fields, enum values, bounds and defaults. Full and Portable core schema normalizations agree. invoke_tool's Eligible signatures are unchanged: several are not available in the hoisted catalog, so removing them would lose argument information. Independent read-only review checked the shortened text against Execute behavior.

This is a modest input reduction, not a measured provider latency/RAM improvement. The separate requested-contract projection addresses prefix-cache invalidation. No model instruction file, provider transport, registry validation, target handler, setting, option, dependency or extra model call is introduced by these description changes.

Ignored evidence: .tmp/goal-tool-descriptions-round6-2026-10-02/{REPORT.md,retained-facts.json,exact-source-replacements.json}; .tmp/goal-output-round6-2026-10-02/media-description-equivalence.json. These artifacts contain the controlled comparison; no private conversation content is copied into this report.
