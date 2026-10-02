# Incremental media history paging

## Confirmed issue and implemented change

Loading an older 60-message page previously rebuilt every loaded transcript row.
This recreated original-size image elements, discarded expanded diagnostics and
repeated all earlier DOM construction. Images use the existing local preview
endpoint; no new thumbnail or cache behavior was introduced.

The older-page path now constructs and prepends only the incoming fragment.
Existing rows, image nodes, expanded output, focus and run-state objects stay in
place. Scroll offset is adjusted by the change in transcript height, including
removal of the final pager. Session opening uses the same history builder instead
of a duplicated implementation. Missing boundary tool arguments are resolved when
the earlier assistant call becomes available. A single return value was added to
`appendHistoryToolPayload` so its old deferred callback can be canceled.

Ordinary live turns already cancel and retire the history pager in `sendPrompt`.
This work is not evidence of a preexisting normal-user live-output-loss bug. The
new synthetic live-state test verifies robustness without changing that guard.

## Deterministic measurement

The dedicated dependency-free Node test retains the former rebuild algorithm as
a reference and runs both paths with the same current renderer and fake DOM.
Ten chronological pages of 60 media results give:

- Image source assignments: 3,300 before; 600 after (81.8% fewer)
- Created elements: 33,025 before; 6,017 after
- Final visible images: 600 in both paths
- Adding 60 results creates exactly 60 images whether 60 or 600 messages are
  already loaded

These are DOM construction/source-assignment counts. They do not measure native
browser layout, image decoding, actual HTTP traffic, GPU memory, FPS, whole-task
latency or process RSS. Loaded message-array concatenation still copies references;
the change removes repeated DOM/media construction, not all paging work.

## Verification

`node --test --test-concurrency=1 test/ui/*.test.cjs`: 134/134 pass on the current
combined checkout, including eleven new paging tests. The dedicated tests cover
retained row/image/output identity, focus, chronological order, simulated scroll
anchoring, deferred boundary inputs, multi-page lookup, worker routing, stale and
failed requests, empty terminal pages and unchanged synthetic live state.

The existing `history-tool-lazy.test.cjs` fake DOM received fixture-only
`insertBefore`, `remove`, parent and sibling behavior so it supports real prepend
semantics. No runtime dependency, installation, build or Go test was run for this
frontend audit. Real Chromium/WebView2 scroll and network profiling remain useful
follow-up validation.

## Identity-reuse verification

Two additional regression tests reproduced failures before their targeted fixes
and pass afterward:

1. Worker mappings are snapshotted before the incoming page renders. A newer-page
   row retains ownership, while repeated IDs appearing only inside the incoming
   page correctly select that page's latest row.
2. A reverse prepass resolves newer-page pending tool results against the last
   matching call in the incoming older page. Forward rendering still pairs each
   result inside that page with its own nearest preceding call. Expanded output
   and image nodes remain unchanged.

## Feature audit at the starting revision

Built-in `read_image`, `send_screenshot` and `show_media` cover image input, native
screen/clipboard capture and local image/video/audio presentation. Audio/video
players remain click-created, non-autoplaying and released when closed. Images
remain lazy-loaded original files in small cards, with no thumbnail service.

There was no built-in image/video generation provider pipeline or native desktop
mouse/keyboard action loop at the starting revision. An iframe URL preview and
optional externally configured MCP capabilities do not establish those built-in
features. Release 1.0.2 explicitly excluded image/video generation. Changes by
other workers after this audit are outside this feature inventory.
