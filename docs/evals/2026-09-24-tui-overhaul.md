# TUI overhaul — 2026-09-24

## Result

- Stable full-screen menu frame with breadcrumbs, highlighted selection, category tabs, a visible position counter, and a detail column in wide terminals.
- Action centre, models, sessions, providers, provider templates/auth/forms, projects, reasoning, goals, context limit and settings use the shared list/detail renderer. Narrow terminals retain the selected row and switch to a single column.
- Settings are grouped into General, Agents, Context and Advanced. Darwin/best-of-N remains functional, but appears only under Advanced alongside specialty controls. Reset-all still resets every managed category.
- Reasoning confirmation updates the persistent header immediately and temporarily replaces the existing hint line. It does not insert/remove a row. Success is green; an older timer cannot clear a newer message. Applying a selection returns to the parent menu and retains its search/draft.
- ANSI-aware truncation protects colored status bars and wide Unicode text. No-color explicitly selects the ASCII color profile.
- Provider API-key masking uses the field index, not the translated label (the old Polish label bypassed masking). Reveal remains explicit and ends on field navigation.
- Removed the stale USERPROFILE storage hint from Projects; no new profile/system storage.
- Additional Polish translations for displayed default values.

## Verification

Passed:
- go test -timeout=90s ./...
- go vet ./...
- git diff --check
- New behavioral regressions cover reasoning/header/draft stability, stale notification timers, parent-menu restoration, EN/PL credential masking, settings tabs during editing, ANSI truncation and selected rows/errors in small terminals.
- Layout tests cover 32×10, 48×12, 80×24, 120×35 and 160×45; PL/EN; colored and no-color modes. Previous populated-menu/navigation tests remain passing.
- Visual inspection of actual Go Model.View() output rendered as ANSI-to-HTML frames in the in-app browser, including the action centre, settings and a small invalid-input form. This is renderer verification, not a live model session in a native terminal.

There are no additional prompts, model requests or provider transport changes. OpenCode Zen's dedicated route was not modified.

Reproducible preview fixture and check outputs are under .tmp/tui-overhaul/ (inside the application directory). Existing unrelated work was preserved.

Installed CLI: 30,483,968 bytes; SHA-256 3f1e781082a9b7f0290b9f91c4d2ae7c46fab9968a57e303a579efb9d2ccdccc. Previous executable: .tmp/previous-tui-binary-RihmUJ/. The GUI executable was unchanged.
