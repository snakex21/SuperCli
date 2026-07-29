# Provider icons

Bundled SVG logos for the provider picker and the configured-providers list in
the web GUI. Served from `icons/providers/` (embedded via `//go:embed assets/*`).

## Naming convention

One file per provider, named after the provider slug plus `.svg`:

```
openai.svg
anthropic.svg
deepseek.svg
xiaomi.svg
```

The slug is the provider `Name` lowercased with anything outside `[a-z0-9-]`
removed.

## Family fallback

Regional variants reuse the family icon automatically. The loader tries the full
slug first, then the part before the first `-`:

- `xiaomi-tokenplan-global` → `xiaomi-tokenplan-global.svg`, then `xiaomi.svg`
- `dashscope-tokenplan-cn`  → `dashscope-tokenplan-cn.svg`, then `dashscope.svg`

So dropping a single `xiaomi.svg` covers every `xiaomi-*` variant. Add a
variant-specific file only when you want it to differ from the family.

## No icon?

Any provider without a matching file shows a colored monogram (first letter,
color derived from the name). Nothing to configure — a new provider is never
left blank.
