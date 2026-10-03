# Tool-read images and portable disclosure preferences

Base: main 9a930aa (1.0.4-dev.17). This frontend change implements explicit image presentation behavior; it does not change model prompts, media bytes sent to providers, or tool specifications.

## Behavior and provenance

Native model-input images (read_image and native MCP image results) now belong inside their expandable tool card. A folded card creates neither an img element nor an image src. The existing once-open payload helper creates the bounded transcript thumbnail when the card opens; reopening retains its viewer control and image node. Consumed path lists and lazy-render callbacks are released after materialization.

Canonical user-visible presentation remains outside the folded card: show_media, send_screenshot, generate_image/video, headless_control screenshots, and owned process_session screenshots keep their existing validated preview contract. Optional analysis images from the same result do not add a second preview. Ordinary user attachments keep their existing behavior.

Restored pages consume only the backend's explicit tool_image_carrier=true and tool_images entries with source_call_id/path. No image classification uses assistant text, carrier Content, or a guessed intention. Genuine/mixed/unmarked legacy messages remain ordinary user attachments; pre-existing sessions without this provenance cannot be retroactively classified reliably.

The incoming page indexes actual tool-result rows by exact call ID and sequence. A carrier binds to its nearest strictly preceding result, including reused IDs. If the source result lies on an older page or was pruned, a temporary tool preview card keeps the image available. Loading its origin transfers the same preview node and removes the temporary card. Only unresolved associations live on the existing pager, and removing that pager/session releases that ownership; there is no global image/history cache.

The history payload guard is now explicit (_historyPayloadRendered). A transferred, already opened image can fill a body before native details.toggle dispatches its older output renderer. Treating any body child as a completed output would otherwise discard the raw output when the preceding call arrives. Three-page and delayed-toggle tests cover this ordering.

Backend provenance persistence, public attachment admission, and provider request parity are evaluated separately in [Tool image provenance](2026-10-03-tool-image-provenance.md).

## Disclosure settings

The existing portable UI settings blob stores toolsExpanded (boolean) and thinkingExpanded (boolean or null). Invalid loaded values normalize to false/null. Unset thought preference preserves the existing live-open/history-closed defaults. Explicit choices apply to future live/restored tool rows and to native and inline thinking blocks. The user's existing choice within a streamed block survives later paints.

Only outer user summary activation persists the new preference. Nested details, buttons, programmatic opening, rendering, and restoration save nothing. Native keyboard summary activation follows the same existing click path. Shift+E and Shift+T save once for the complete bulk operation, independent of later asynchronous toggle events. The existing saveUI debounce and /api/settings merge remain; no new timer, panel, button, browser-storage write, or command was introduced. Legacy localStorage migration remains read-only.

Expanded live tool defaults initialize private fields before materializing their lazy input. This preserves complete input text and the earlier release of consumed raw arguments.

## Verification

Focused Node UI controls: **64 passed, zero failed**. They include previous media/history/lazy/startup controls plus new exact raw output, three-page source/result/call association, delayed toggle, reused/future IDs, explicit presentation, legacy/mixed fallback, reload/normalization, future rows, single/bulk persistence, and native/inline thought choice.

An isolated owned headless Chrome fixture ran successfully (exit 0), using a fresh profile/cache/temp/crash directory inside the repository. No user application/profile, user image, provider, or external site was accessed. External requests were aborted; only generated same-origin thumbnail responses were fulfilled.

Observed native controls:

- Folded read: zero image elements, zero body children, zero image requests before opening.
- Real mouse summary click: opens the card and produces one portable settings patch with toolsExpanded=true.
- Native Space summary activation: closes it and produces one further patch with false.
- Programmatic reopening: no settings patch; image and viewer button identities remain unchanged.
- Original viewer: uses the exact session image path without the thumbnail parameter.
- show_media: remains a visible sibling, and optional native analysis adds no duplicate.
- Shift+E across two cards: one further settings patch, with both cards closed.

Ignored evidence: .tmp/goal-tool-media-presentation-2026-10-03/native-presentation-fixture.cjs, native-presentation-results.json, native-presentation-run.json, and frontend-frozen-manifest.json. The full UI/integration suite is run by the integrating agent after this source freeze.

These are deterministic behavior, source ownership, and native DOM controls. They do not measure WebView2 RSS, layout FPS, inference speed, or token savings. The browser fixture uses generated media; it does not establish performance for arbitrary real images.
