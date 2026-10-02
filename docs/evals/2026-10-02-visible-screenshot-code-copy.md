# Visible screenshot previews and copying code — 2026-10-02

## Trigger and cause

A user requested `cześć zrób zrzut pulpitu mi`. After the screenshot schema visibility repair, the provider called `send_screenshot` and saved the file, but the chat showed only the tool summary and a path from the model. `appendToolPayload` created the preview only when the tool details opened; recent transcript DOM deferral kept those details collapsed.

## Change

Strict tool-result metadata is parsed separately from diagnostics. A recognized local screenshot or media file creates one sibling preview below its tool row, outside collapsed details. Live results, resumed sessions and older-page rendering use the same path. Existing MIME validation, local-path restrictions and portable `snapshot:` tokens remain. No model request, image-generation provider or new dependency is needed. Images are lazy; audio/video create a player only on explicit click. Raw input/output continues to render lazily inside details.

A Copy button is generated alongside fenced code. A single click listener on the transcript serves all code buttons; growing code adds its button only when the existing code node is created. Clipboard text is taken from that block’s `code.textContent` at click time. Pending clicks are deduplicated. Browser Clipboard API failure falls back to a temporary local textarea; it is removed and focus/selection restored in all outcomes. Errors are reported without claiming success. All 27 GUI catalogs include the labels.

## Validation

- 123 GUI tests passed: folded live previews, restored history, MIME/path rejection, moved portable snapshots, no duplicate media after detail toggles, no auto playback, exact Markdown prefix equivalence, stable growing code nodes, Unicode/indentation/newline copy, pending-copy behavior, fallback cleanup and failure feedback.
- `go test ./internal/webgui ./internal/tools/media -count=1` passed.
- README structure and translations remained equivalent after version update.

These UI changes do not reduce the resident memory of WebView2 itself and do not provide desktop click/keyboard control.
