# Adaptive reasoning controls — 2026-09-23

GUI and TUI now derive their menus from the same provider reasoning state.
A model whose native metadata declares an on/off control receives Default, Off
and On. Legacy positive settings (for example low) select the single On item;
they no longer appear to promise different thinking budgets. Models with learned
API-supported effort values show that supported subset. Unknown support retains
the existing selectable values and negotiation behavior.

The state distinguishes configured, effective and selected values. Existing
wire values remain unchanged: the On menu item uses high, and Off uses none.
OpenCode Zen's special transport, headers and reasoning serialization are not
changed by this UI work.

## Persistence and ordering

- GUI choice changes save the active session runtime immediately, without a new
  user message and without changing message count or conversation timestamp.
- Missing sessions, sessions outside the active project and invalid values are
  rejected before changing runtime.
- A pending reasoning save participates in the existing session-runtime promise,
  so sending a prompt or restoring another conversation waits for it.
- Responses to older model/reasoning reads cannot repaint the selection after a
  click. Older session-list responses cannot overwrite the updated runtime cache.
- TUI menu saves use the provider manager to update the active project config as
  well as the app-local global config.
- A malformed config produces an error and is preserved rather than replaced.
  GUI reports config persistence failures after an otherwise applied selection.

These changes add no prompt instructions or model requests. Menu state is derived
locally. Session metadata is saved during the existing reasoning-setting request.
This is a correctness/usability change, not a measured model-speed benchmark.

## Validation

- `go test ./...`: passed.
- `go vet ./...`: passed.
- `git diff --check`: passed (only existing Windows line-ending notices).
- TUI tests cover adaptive entries, legacy selection, keyboard navigation, actual
  request values and active/global config persistence.
- Backend tests cover immediate session persistence, default restoration,
  invalid/foreign session rejection, learned supported levels and malformed
  config preservation.
- `scripts/test-reasoning-ui.cjs`: passed using installed Chromium headless shell
  1228 with a dedicated app-local profile and a loopback fixture serving the real
  GUI assets. Covers Polish labels, radio selection, keyboard navigation,
  reopening a session without another message, a delayed catalog response,
  switching conversations during a pending save, and graded effort choices.
- Screenshots visually checked at 1250×900 and 520×900; menu stays within viewport.

The GUI fixture does not call an LLM. Provider-side compliance with a graded
effort remains distinct from successfully sending that setting; this test does
not claim new live validation of Muse or other cloud backends. The prior live
local-model measurements are in `2026-09-23-reasoning-control.md`.

Test artifacts: `.tmp/reasoning-ui/` and
`.tmp/reasoning-ui-validation.json`, kept inside the application directory.

## Follow-up: cached inventory skipped native discovery

A user screenshot showed the full effort list for LM Studio's
`qwen3.8-27b-uncensored`. The earlier browser fixture supplied already-known
capabilities, so it did not expose this startup integration bug.

The GUI's `cachedModelCount` includes saved model IDs and the configured model,
but neither proves that native reasoning metadata has been loaded. Consequently
`handleModels` skipped scanning on startup and the registry retained only
heuristics. A new regression reproduced the screenshot's state: configured
xhigh, effective xhigh, toggle_only false, and zero metadata requests.

Both reasoning and model-list API handlers now ensure native reasoning metadata
for the active local OpenAI/Responses endpoint. Concurrent requests share one
bounded attempt per endpoint/credential pair per Engine. The attempt has a
2-second total deadline, makes no inference request, does not scan cloud
providers, and does not retry on refresh after failure. Explicit Scan can still
refresh capabilities. Vision, tool and context metadata are preserved.

Validation after the fix:

- Cached-inventory regression: passed, including eight concurrent GUI reads and
  exactly one native metadata request.
- Unavailable native API: passed, one v1/v0 attempt and no refresh retry loop.
- Metadata-only integration against the user's running LM Studio: passed.
  Actual GUI response: configured xhigh, effective on, toggle_only true,
  levels [none, high]. The frontend displays these as Off and On.
- Full Go suite: 64 packages passed; go vet and diff whitespace check passed.

This verifies discovery and GUI response construction without spending model
tokens. It does not claim that xhigh provides a larger budget on this binary
on/off backend.
