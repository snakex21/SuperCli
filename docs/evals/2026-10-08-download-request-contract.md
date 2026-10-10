# Direct download request contract, 2026-10-08

`web_download` already streamed public files into the workspace, but a direct
download request did not expose its contract until tool discovery. An isolated
live Kilo/Ling run instead attempted shell downloads, including an invalid
command-array argument before a successful PowerShell request.

An explicit download action plus an HTTP(S) URL now exposes the registered
`web_download` contract for that run, using the existing requested-tool context.
It does not execute the URL, choose a destination, activate the tool permanently,
or add schemas/instructions to ordinary turns. URL path words alone and the
covered question/negation forms do not trigger it. General asset search keeps
ordinary discovery. The native fallback for embedders without `invoke_tool`
includes the real schema and invalidates its snapshot when the allowance ends.
Target validation, public-address checks, file-size limits, atomic publication,
and refusal to overwrite remain unchanged. OpenCode Zen transport is unchanged.

Regression tests check unchanged core schemas/cacheable prefix, exact current
description/schema, request-token estimation, history/discovery isolation,
next-run expiry, restricted/final-only registries and target validation. A
public Loop fixture completes a direct request with one download invocation and
two provider calls, without discovery. Full Go tests and vet subsequently pass.

## Live evidence and limits

Tests used separate portable synthetic data/workspace folders and an anonymous
Kilo endpoint through a loopback relay (thin-tool profile). The public PNG was
7,789 bytes; its SHA256 was
`863e70e6cb473981b81bbe1d700dc7f0912d14e43fd068652bf427fa94594a74`.
No user chats, credentials, projects or goals were loaded.

- Installed baseline/Ling saved the exact file after two shell attempts, then
  encountered HTTP 429 before the final response.
- Changed Ling attempt received HTTP 429 before any model response. This is not
  a successful speed comparison.
- Changed Nemotron selected `web_download` on its first response and saved the
  exact file. It then attempted a binary text read, a duplicate download and a
  shell hash command, hitting the isolated four-step safety limit. The harness
  correctly refused the duplicate without another HTTP request.

Thus the live test proves first-call download availability and file integrity,
not reliable end-to-end completion or a measured reduction in model turns.
No repeated-call penalty, forced completion rule or extra permanent prompt was
introduced to disguise those model choices. Raw aggregate receipts are retained
under ignored `.tmp/optimization-oct8-2026/live-download`.
