# 2026-10-01 — site preview and remaining audit changes

## Delivered

- One lazy GUI pane; a TUI action with a separate URL editor and two choices.
- Shared HTTP(S) normalizer and native/default-browser opener without cmd/start.
- A standalone --preview branch before engine/provider/session initialization.
- Portable preview profile; existing PDF frames preserved; foreign origins/app
  framing denied.
- 12 matching new keys in GUI/TUI catalogs for all 27 supported languages.
- Removed the two unused filesystem wrappers in session/store_fs.go; their
  standard-library calls are now direct. No claim of speedup for that cleanup.
- Discovery-facing schema JSON compaction without changing registry/provider
  schemas, signatures, activation or ordering.
- Optional reasoning replay fields are decoded only when present.
- Context aliases resolved against the live catalog under its read lock without
  copying or sorting the catalog; exact-match precedence and ambiguous suffixes
  remain unchanged.

## Controlled measurements

These are helper/output measurements, not end-to-end latency/token claims.

- Production context-alias lookup, 512 models: old 96,968 ns/op,
  65,736 B/op, 4 allocs/op → new 11,465 ns/op, 0 B/op, 0 allocs/op.
  A 20-model catalog: 2,525 → 537 ns/op; exact lookups remain about 36–39 ns.
- Plain native replay packet: median of three 500 ms runs, 273.8 ns/op,
  552 B/op, 9 allocs/op → 29.12 ns/op, 0 B/op, 0 allocs/op.
- Actual discovery response bytes: thunderbird_mail 8,596 → 8,090;
  edit_docx 6,151 → 5,741; outlook_mail 2,986 → 2,736;
  goal 1,852 → 1,579; patch_file 1,567 → 1,229.
  Thunderbird now fits under the existing 8,192-byte inline threshold in this
  fixture. No measured token or first-token percentage is inferred from bytes.

## Validation

- Go packages: llm, agent, search, storage/session, app, webgui, tui, uilang,
  system/browser and cmd/supercli-web passed.
- Native WebView2 GUI: 12 checks passed, including a real local module script,
  cross-origin API rejection, app-redirect/frame rejection, draft retention,
  header bounds, and pane/frame disposal.
- Native standalone viewer: 6 checks passed, including localization and absence
  of agent API/routes. Both tests use private portable fixtures and echo where
  an engine is needed; they never call a cloud/local model.
- URL normalization unit/fuzz checks and Windows/Linux/macOS test cross-builds
  passed. TUI launch tests use injected launchers/fake processes.
- JavaScript regression suite and catalog parity pass. The existing discovery
  test now verifies compact result bytes and unchanged registered schemas.

Local reproducible fixtures/reports are in .tmp/site-preview-2026-10-01,
.tmp/cli-audit-followup-2026-10-01, .tmp/harness-schema and the audit directories.
The first native GUI fixture runs exposed two fixture issues (copied original
Content-Length and a programmatically changed input without its input event);
after those corrections the full native GUI probe passed.

## Limits and deferred findings

Previewing is opt-in. A loaded site consumes renderer resources; no claim that
its open state costs zero memory, or that closing immediately returns all cached
browser memory. Sites with frame restrictions use the external-browser action.
TUI reports process-start success; later GUI initialization failures are not
relayed back to TUI.

The audits also identified full TUI View work, typed-plus-raw chunk decoding and
durable terminal barriers. These were left unchanged because removing them needs
broader invalidation/protocol/durability work, not a speculative shortcut.
