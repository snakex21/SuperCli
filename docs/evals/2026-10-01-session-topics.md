# Session topic titles and unchanged sidebar refreshes

Date: 2026-10-01

## Cause and changes

The deferred conversation-title job used a pull-request summary prompt asking for completed changes in the first person. A greeting therefore produced labels such as “I haven't made any changes yet”. The job now asks for the user's topic in the same language, as a single short title. The request contains at most 512 characters of normalized topic text rather than the full first message.

Short prompts (at most 80 visible Unicode characters after Markdown/code normalization) retain their deterministic local label. They create neither a deferred title timer nor an extra provider request. A manual rename also skips inference. A completed provider failure/equal-label fallback is terminal; foreground cancellation may still retry after the existing idle delay. The foreground agent prompt and OpenCode Zen transport are unchanged.

Generated title writes keep the conversation activity timestamp. Their compare-and-swap still preserves manual renames.

The GUI caches its last displayed session metadata snapshot. Identical refreshes preserve row DOM, focus and hover state. Changed metadata, selection, locale or minute labels still render; stale responses remain ignored, and a failed request invalidates the snapshot so a successful retry restores the list. No polling, timers or provider requests were added for this cache.

## Validation

- All 67 UI tests passed, including four new sidebar tests for identical data, changed data/selection/locale/time, failed-fetch recovery, and overlapping stale responses.
- Full Go tests passed for internal/webgui and internal/storage/session. Added tests cover skipped inference, manual names, terminal failure, Unicode/code normalization, bounded title input, and unchanged session ordering. The final focused tests also passed after sharing the title-length constant with session creation.
- A live Zen space-bunny-free probe returned topic titles in both languages: “Nazwy sesji opisujące temat i stabilna lista rozmów w SuperCli” (2810 ms) and “Improve SuperCli conversation titles and sidebar refresh behavior” (1541 ms). These are title-only background calls, not foreground answer timings.
- The saved LM Studio configuration returned the deterministic fallback immediately; live local-model title generation was not confirmed. Offline and unavailable-provider behavior is covered by the fallback tests.

## WebView2 comparison

A separate portable native fixture loaded the real embedded GUI and compared the saved pre-change sessions script with the changed script. Its API stub returned the same 40 sessions for 100 refreshes. Each iteration included a layout read. Network/provider latency is excluded.

| Measurement | Before | After |
| --- | ---: | ---: |
| Total for 100 refreshes | 62.4 ms | 5.7 ms |
| Per-refresh p95 | 1.0 ms | 0.1 ms |
| List child mutations | 4200 | 0 |
| Original first row retained | no | yes |

This measures one unchanged-sidebar workload, not total application speed or all frame timings.

## Existing local data

Two stored labels explicitly shown in the user's screenshot were found and repaired to their original first-message label using compare-and-swap. A title-only backup was saved under the portable data folder's backups directory. Both activity timestamps were verified unchanged. The Polish label shown in the screenshot was not present; other stored names and conversation contents were left alone. No automatic language-specific title-rewriting migration was added.

Both Windows GUI and TUI executables were rebuilt at the existing 1.0.1 version.
