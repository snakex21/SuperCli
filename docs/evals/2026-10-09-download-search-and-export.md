# Download search and requested exports, 2026-10-09

This records the earlier Downloads fix. The later natural-follow-up failure,
generic tool handoff and real three-turn GUI evaluation are documented in
[download-followups](2026-10-09-download-followups.md).

The GUI could find a public Tenor GIF but could not save it to the user's
Downloads folder. Both the downloader and checkpoint admission required a
workspace destination, even with the application's existing allow-all setting.
Repeated attempts therefore made no HTTP request and could never succeed.

Human-facing TUI, GUI and batch loops now resolve an explicitly requested
Downloads destination using the Windows known folder API. A narrow direct
instruction creates a context-scoped download-only folder grant. Generated
worker prompts cannot create grants; workers may inherit the authorization of
their current parent task. Unrelated subsequent human runs replace the grant.
Application state remains in the portable data directory; only requested user
output is exported.

The downloader retains public URL checks, byte limits, cancellable streaming,
SHA256 measurement, adjacent temporary files and atomic publication without
overwrite. Canonical export roots and checkpoint destinations are pinned and
rechecked. An authorized new external file does not create a project checkpoint;
workspace downloads retain exact Undo/Redo. Other filesystem tools do not gain
access from the export grant.

An explicit download exposes its exact contract without tool discovery. Search
and page-link downloads also expose web_fetch; direct media URLs retain the
smaller downloader-only contract. Self-contained public web actions skip the
navigator model and defer pending repository preflight. Mixed coding/command
tasks, local searches, attachments, explanations, quotations and negations keep
the ordinary route. Contracts remain transient, use the current registry, are
included in request estimates and do not change the stable system prefix or
persist in history. The search-and-download contracts cost about 634 estimated
tokens per request; they avoid two discovery round trips in the covered flow.

## Live evidence

The real batch executable, a loaded local qwen3.8-27b-uncensored endpoint and
public Tenor/search pages completed a no-URL Polish instruction to save a GIF
into the actual Downloads folder. State and configuration were isolated in
.tmp/download-optimization; thinking was off only in this test configuration.

- Wall time: 21,437 ms; process exit 0.
- Four main model requests; 18,038 input and 341 output tokens.
- Three tools: web_lookup, web_fetch, web_download. No tool_search, navigator,
  shell download, duplicate write, or binary text read.
- Export: supercli-tenor-test-1791574868082.gif, 172,211 bytes, GIF89a.
- SHA256: 2daa34ee592235c00467cea0fbb42120cb334f62f1984c73826cf8500077f4b6.

The raw receipt is .tmp/download-optimization/qwen-receipt.json. This is one
successful real task, not a success-rate estimate or an A/B speed comparison
against the cloud model in the screenshot. Models remain free to make additional
searches; no forced-completion or hidden automatic URL selection was added.

## Accounting

The screenshot's session total, cached input and evaluated input match the
provider usage records: 84,638 session tokens. The displayed generation speed
uses output tokens divided by streaming time, excluding time to first token.

A separate daily-total bug added matching TUI main usage and loop credit-ledger
copies twice. The read-only daily aggregation now consumes each mirror pair
once. Old databases have no shared call identifier, so matching is deliberately
conservative: same session/input/output, main then parentless loop within two
seconds, before the next main call. Unmatched, delayed, helper and ledger-only
records remain counted. No history or budget is rewritten. Replaying the
screenshot's databases yields 610,199 instead of 611,346 daily tokens, removing
one 1,147-token mirror. Existing timestamp indexes bound both daily queries.

Tests cover actual checkpoint-wrapped downloads, scope expiry, delegated scope,
no overwrite/no HTTP, junction changes before and during download, cancellations,
workspace Undo/Redo, model/request counts, registry isolation and daily mirror
edge cases. Integration validation additionally exercises native Stop and
completion in TUI and GUI.

Final checks pass for agent, the complete checkpoint package, all modified tool
packages, credits, session storage, TUI, WebGUI, app and LLM packages. Vet passes;
all 235 JavaScript UI tests pass with zero skips. The first broad checkpoint run
hit its aggregate 180-second package limit near the final tests; the standalone
run with a 600-second allowance completes in 170 seconds. An unrelated native
window-capture foreground assertion failed during concurrent package testing;
its isolated rerun passes, without changing capture code. GIF decoding confirms
17 frames at 220 x 229 pixels.

Both installed executables have verified backup hashes and pass --help smoke
checks. Installation evidence is .tmp/download-optimization/installed.json and
help-smoke.json; existing application sessions and settings were preserved.
