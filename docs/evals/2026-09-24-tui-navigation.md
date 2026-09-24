# TUI navigation — 2026-09-24

The action centre now has six keyboard-selectable categories: All, Work, Model,
Files, Agent and System (with Polish labels). Left/right or Tab/Shift+Tab changes
the category; up/down and Enter select an action. Typing filters the current
category. Changing category clears that category's search.

Esc returns to the previous screen, preserving its cursor, category and filter.
Inline settings/queue/data edits cancel before leaving the screen. Ctrl+K closes
the menu hierarchy and restores focus to the existing conversation draft.
Completed provider forms are removed from the return path. Returning to the
provider list does not rerun its connectivity probes.

Cold model discovery now runs in a Bubble Tea command. The initial menu, typing,
and Esc remain available while the server responds. Reopening either picker
while that request is pending does not launch another picker scan. Existing
startup discovery and explicit provider refresh behavior are otherwise unchanged.
Model names starting with j/k now filter correctly from the first character.

The action centre uses one row per item plus a selected-action description.
Lists respect small terminal heights; the extra blank line before the settings
reset action no longer pushes the return hint off-screen.

## Validation

- Full go test ./... and go vet ./... passed; git diff --check passed.
- Regression coverage: category/search/back/draft preservation, nested provider
  form cancellation, completed-form navigation, settings edit cancellation,
  first-letter model filtering, discovery with an intentionally blocked HTTP
  response, and rendering/navigation while that response is pending.
- Populated action, model, session and settings panels checked at 48x12, 48x18,
  64x16, 80x24 and 120x40, in Polish and English, at the beginning and end of lists.
- Actual View() text snapshots reviewed in .tmp/tui-navigation. These exercise
  the TUI renderer and key handlers; they are not a live terminal emulator test.

No agent instructions or model prompt changes. No additional inference requests.
Provider discovery still uses the existing model-list endpoints and policies.
The special OpenCode Zen transport path was not modified.

## Functional coverage and language follow-up

The centre additionally exposes per-provider/model context budgets (presets and
custom input), context inspection/compaction, project memory, ChatGPT accounts
and Markdown export. Export now selects the active session by ID instead of the
most recently updated unrelated session, and defaults to the application data
folder when configured.

The goal screen supports creating a goal with description and success criteria,
adding named steps, toggling steps, notes, explicit verification evidence, verified
completion, pause and resume. These use the same storage/service rules as the GUI;
incomplete or unverified goals cannot be marked complete through the new menu.

Both surfaces already supported PL/EN. Fixed the TUI's Polish-only relative dates
and the native GUI close confirmation. Dynamic translated GUI elements now retain
a reference to their label text node: language changes update that node without
replacing nested controls, entered values, icons or generated conversation text.
Model context/reasoning labels and session groups also refresh on a language change.

Functional parity means shared operations, not identical presentation. TUI still
uses slash commands for some advanced operations (for example memory search/forget
and MCP management); GUI-specific previews, layout and native pickers remain GUI
features. The existing action/route contract is supplemented by behavioral tests
for goal persistence and verification, scoped context limits and active-session
export. JavaScript regression checks run with node --test test/ui/i18n.test.cjs.
