# TUI: native file selection and obsolete header tier — 2026-09-24

## Changes

- Removed the obsolete small/big tier label from the TUI header. Model routing and tier budgets are unchanged.
- Ctrl+O opens the existing Windows multi-file dialog, now shared by GUI and TUI.
- Files copied in Explorer can be attached with Ctrl+V in TUI. Regular text paste still works.
- Selection preserves the message draft. Cancellation preserves previous attachments. Repeated paste does not remove or duplicate a file.
- Validate file count, regular files, per-file size and total size before accepting a native/clipboard batch. A rejected batch leaves the existing selection intact.
- Files remain pending until the user sends the message. Existing image preparation and attachment persistence remain shared with GUI.
- Ctrl+K → Attachments opens the existing file browser to review/remove selected files, also with a nonempty draft. Ctrl+O is available inside that browser.
- If the native dialog fails, TUI opens its internal file browser with the error. Non-Windows platforms keep the internal browser.
- The application uses no new provider call, prompt instruction, persistent setting, external dependency or background poll.

## Verification

- Full go test ./... passed.
- go vet ./... passed.
- Targeted desktopfiles, TUI and GUI suites passed after the final test-fixture adjustment.
- Tested the actual Windows Shell32 DROPFILES decoder using a memory fixture with Polish names and file limits, without modifying the user's clipboard.
- Tested picker cancellation, failure fallback, preserving a draft, modal input protection, duplicate selection, invalid selections and the obsolete tier label.
- No interactive click-through of the native Windows dialog was performed in this environment. Picker integration uses the GUI's existing GetOpenFileNameW implementation.
- The terminal must deliver Ctrl+V to the TUI for Explorer-file paste. Ctrl+O is the direct file selection route. This change reads copied files, not raw screenshot pixels from the clipboard.

Windows API references:
- [DragQueryFileW](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/nf-shellapi-dragqueryfilew)
- [GetClipboardData](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getclipboarddata)
