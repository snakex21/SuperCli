# Provider icons

Built-in provider logos for the provider list, the provider catalog and the
logo picker in the web GUI, embedded in the executable.

Source: LobeHub Icons (`@lobehub/icons-static-svg` 1.104.0, MIT,
https://github.com/lobehub/lobe-icons). See `THIRD_PARTY_NOTICES.md` and
`LICENSES/icons/lobe-icons-MIT.txt`. Files were reduced to the drawing (no
title, fixed 24x24 size).

## Naming

One file per logo key, e.g. `openai.svg`, `deepseek.svg`. The key is not the
provider's name: the server picks it from the provider type and base URL
(`ProviderIconKey` in `internal/webgui/provider_icons.go`), so a provider the
user named "ja" that points at api.anthropic.com shows the Anthropic logo.

Logos drawn in `currentColor` are single-colour: the GUI shows them as a mask
in the theme's text colour. All others are shown as images.

## No match

A provider without a matching logo shows a coloured letter. Users can pick any
built-in logo or upload their own image in the provider's edit form
(stored under `<data>/provider-logos/`).
