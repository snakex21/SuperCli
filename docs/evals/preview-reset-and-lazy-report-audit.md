# Preview reset, sizing and hidden-report rendering

## Result

The GUI site preview now has Clear, Expand and Restore actions. Clearing removes
the iframe, address and remembered URL while leaving an empty pane open. It also
invalidates outstanding navigation and browser-launch notices; a late response
cannot restore the cleared page.

The pane's left edge changes its width with pointer capture. A focused edge also
accepts Left/Right, Shift for larger steps, Home for the minimum and End for the
maximum width. The layout preserves room for the conversation and accounts for
the docked sidebar and interface scaling. Expand/Restore changes the layout of the
existing iframe without loading the site again. Narrow windows already use the
full available width. The four new control labels are in both sets of all 27
language catalogs.

Closed worker result rows now retain their original source and render their
Markdown only when opened. Updating a closed row cancels its previous deferred
renderer. History reasoning stays folded on its first render. Open rows still
update immediately. This changes display work, not agent context or saved results.

TUI default shortcut hints reuse one bounded cache entry keyed by width, language,
style and renderer color environment. Status notices, scrolling hints, menus,
input text and live agent state remain on their current rendering paths.

## Measurements and limits

| Fixture | Before | After |
| --- | ---: | ---: |
| Create 60 closed worker reports (1,036,020 source characters) | 91.4 ms | 14.6 ms |
| Elements created for those reports | 37,620 | 1,200 |
| Private-memory growth of the fixture process tree | 39,333,888 bytes | 15,376,384 bytes |
| Render default TUI hints, three-run median | 22.5 µs | 3.8 µs |
| Render the complete TUI view, three-run median | 137.3 µs | 126.5 µs |

Opening the first deferred report took 1.8 ms and produced the complete original
content. The roughly 24 MB memory reduction is specific to this hidden-report
fixture; it is not a claim about GUI startup memory.

Hint allocations fell from seven to one, and full-view allocations from 158 to
152. Bytes per hint render rose from 464 to 576; full-view bytes rose from 46,574
to 46,735. The cache reduces CPU work with this small allocation-size tradeoff.
A paced stream delivered all 401 events and 426–427 views. Its noisy end-to-end
timings did not establish an overall terminal streaming latency improvement.

No new model requests, classification passes or prompt instructions were added.
The OpenCode Zen provider route was not changed.

## Cleanup

Four old root diagnostic files were copied, hash-verified and removed:

- `błąd-delegacja.PNG`
- `do zdjęcia błąd delegacja.txt`
- `nie działa co.txt`
- `błędy.txt`

Their combined size was 229,641 bytes. Recovery copies and SHA-256 values are in
`.tmp/diagnostic-cleanup-2026-10-01/manifest.json` beside the original filenames.
No code, build or documentation references were found. The live `do dodania.txt`
wish list and the existing ignore rule were retained.

## Validation

- 92/92 JavaScript UI tests passed.
- Go tests passed for `internal/webgui`, `internal/system/uilang` and
  `internal/ui/tui`.
- Native GUI fixture: 20/20 checks passed, covering resize via keyboard,
  expand/restore with the same iframe, reset, pending navigation, reopen, draft
  preservation and existing frame/API isolation.
- Standalone native preview: 7/7 checks passed, including Polish labels, isolated
  development-page loading, no agent APIs and reset to an empty pane.
- Worker-report native fixture: 8/8 checks passed.
- Pointer dragging uses native Pointer Events and capture; the native resize
  fixture exercised the keyboard path rather than physical mouse input.

The first layout fixture ran below the responsive breakpoint after Windows DPI
scaling. A wider test window exercised the desktop resize/expand path; the narrow
full-width layout remains intentional.

Local raw results live under `.tmp/preview-reset-resize-2026-10-01`,
`.tmp/audit-cli-evidence-2026-10-01/gui-task-report` and
`.tmp/audit-stream-latency-2026-10-01/tui-followup`.

Windows GUI/TUI builds passed and replaced the local executables at version
1.0.1. Previous binaries were hash-verified and retained in the local trial
folder. Linux amd64 and macOS arm64 builds also passed for both entry points;
those cross-builds were not run on their target operating systems.
