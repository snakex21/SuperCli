# Cline / Nous Portal presets and a single free-catalog snapshot

## Provider setup

GUI, TUI and first-run setup share two new presets:

| ID | Display name | Protocol | Base URL |
| --- | --- | --- | --- |
| `cline` | Cline | OpenAI Chat Completions | `https://api.cline.bot/api/v1` |
| `nous` | Nous Portal | OpenAI Chat Completions | `https://inference-api.nousresearch.com/v1` |

Descriptions in all 27 GUI/TUI catalogs state the credential requirement. The GUI
form retains the selected preset description, so it stays visible after choosing
the provider. Brand display names are separate from persistent provider IDs.
Selecting a preset pre-fills the existing form; these entries do not trigger
automatic requests to a new provider.

Cline's [authentication documentation](https://docs.cline.bot/api/authentication)
requires a Bearer API key or account token. The documented endpoint is in the
[API overview](https://docs.cline.bot/api/overview). Nous documents its inference
endpoint and authenticated setup in the
[Portal integration](https://hermes-agent.nousresearch.com/docs/integrations/nous-portal).

Anonymous live checks on 2026-10-02:

| Request | Cline | Nous Portal |
| --- | --- | --- |
| GET models without Authorization | 200, catalog available | 200, catalog available |
| POST Chat Completions with a current free model, without Authorization | 401 | 402 |
| Result | Account/key required | Authorization or x402 required |

The Cline inference check used `apodex/apodex-1.1-mini:free`; the Nous check used
`poolside/laguna-xs-2.1:free`, both returned by the current catalog. Nous's 402
response explicitly required a valid Authorization header or x402 payment, even
though the offered payment amount for that model was zero. Neither check
generated a completion. An earlier Nous check with an obsolete model ID returned
404 and was not used to judge authentication.

Public catalog access therefore does not establish anonymous inference support.
These presets are not advertised as a no-key free tier. Setup uses the existing
Bearer credential field; automatic Cline/Nous OAuth and x402 payment are outside
this change. Authenticated inference was not tested because no credentials were
supplied.

## Catalog efficiency and correctness

A cold anonymous free-provider scan previously called `ListFreeModels`, then
`ListProviderModelInfos` separately. The second request could also be replaced
by a previously cached metadata response, pairing a fresh model inventory with
stale capabilities.

`ListFreeProviderCatalog` now supplies free IDs and optional capabilities from
one fresh HTTP response. The old ID-only discovery call still avoids parsing
unused capability metadata. Explicit free flags, complete zero pricing, ID
labels, headers, public credential defaults, timeouts and body limits retain
their existing behavior. Malformed optional capability fields leave validated
free IDs usable with the existing heuristic fallback.

A local regression fixture failed before the fix: a cold scan made two standard
catalog requests and used capability metadata from the second response. After the
fix it makes one request, and a second scan gets fresh metadata from its own
response. Local native metadata queries are tested separately from the standard
catalog counter.

This removes one network round trip from a cold free-provider scan. It does not
establish a reduction in model generation time, prompt tokens or GUI startup RAM.

The OpenCode Zen-specific transport and payload rules were not modified.

## Validation

- 92/92 JavaScript UI tests passed.
- Go tests passed for `internal/llm`, `internal/llm/providers`,
  `internal/system/uilang`, `internal/ui/tui` and `internal/webgui`.
- The new scan test covers request count, fresh metadata on rescan, vision,
  reasoning/context metadata and exclusion of paid/non-free preview entries.
- A malformed optional-metadata test preserves free IDs instead of rejecting
  an otherwise valid catalog.
- Existing preset parity, localization, anonymous filtering and Zen transport
  checks passed with the changes.

Raw anonymous responses and test outputs are retained locally under
`.tmp/provider-presets-2026-10-02`.

Windows GUI/TUI builds passed at version 1.0.1, as did Linux amd64 and macOS
arm64 builds for both entry points. Target-platform runtime testing was not
performed. Both local Windows EXEs were updated and SHA-256-verified after the
GUI window was closed. Verified previous-binary backups are in the trial folder.
