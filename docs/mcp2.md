# MCP 2 in SuperCli (1.0.19)

SuperCli speaks plain MCP (protocol `2025-06-18`, JSON-RPC over stdio). Any
standard MCP server works unchanged. "MCP 2" means optional extras only:

- SuperCli extensions live in MCP `_meta` under keys prefixed `supercli/`,
  or in the portable `manifest.toml`. Other clients ignore them.
- Servers that use no extras behave exactly as before.

## Portable manifest (`<data>/mcp/<package>/manifest.toml`)

```toml
schema = 1
name = "scene"
description = "Scene inspector"
command = "bin/scene-mcp"          # relative to the package folder, or on PATH
args = ["--root", "${MCP_DIR}"]
tags = ["3d"]
image_mode = "preview"             # package default: model | preview | file
session = "conversation"           # optional, see "Sessions"

[[tools]]                          # optional: discovery without a process
name = "look"
description = "Render the current view"
input_schema = '{"type":"object","properties":{"camera":{"type":"string"}}}'
read_only = true
image_mode = "preview"

[[tools]]
name = "build"
description = "Create an object"
read_only = false
[tools.input_schema]               # a TOML table is accepted too
type = "object"
[tools.input_schema.properties.size]
type = "integer"
```

| Field | Meaning |
|---|---|
| `[[tools]]` | Declared tool list. `mcp_bridge` search/list uses it **without starting the process**; the process starts only on `call`. Once the server is running, its real `tools/list` wins and is used from then on. Without `[[tools]]`, search starts the server lazily as before. |
| `tools.input_schema` | Inline JSON string or TOML table; must be a JSON object. Defaults to `{"type":"object"}`. |
| `tools.read_only` | Explicit read-only declaration (overrides MCP annotations, both ways). |
| `tools.image_mode` | Per-tool image mode hint (same as `_meta["supercli/image_mode"]`). |
| `image_mode` | Package-wide image mode (wins over tool hints). |
| `session` | `"conversation"` or empty. Any other value marks the package unavailable. |

A malformed `[[tools]]` entry marks only that package unavailable, with the
error shown in diagnostics.

## Read-only hints

SuperCli honours standard MCP tool `annotations` from `tools/list`:
a tool counts as read-only only if `readOnlyHint: true` **and** not
`destructiveHint: true`. A manifest `read_only` wins. Missing or unknown
hints mean "not read-only" (the current, conservative behaviour). Read-only
MCP tools are scheduled like other read-only SuperCli tools (for example,
parallel read batches). `mcp_bridge` search results include `"read_only": true`.

## `_meta` keys

| Where | Key | Meaning |
|---|---|---|
| tool (`tools/list`) | `supercli/image_mode` | Per-tool image mode hint. |
| `tools/call` request | `progressToken` (standard) | Always sent; enables progress. |
| `tools/call` request | `supercli/session_id` | Conversation id, sent only when `session = "conversation"` and an id is known. |

## Image modes

Precedence: server/package config > tool hint > global `[mcp] image_mode` >
`model`.

- `model`: the model receives the pixels, the user sees a preview, plus a path note.
- `preview`: the user sees a thumbnail. The model gets only a text line with
  the saved path. Thumbnails are saved with the tool result, so they also
  return when an old session is reopened.
- `file`: saved to session media. The model gets the path and there is no preview.

Binary non-image resources are always saved as files.

## Batch calls

`mcp_bridge` `action=call` accepts `calls=[{server,tool,arguments}, ...]`
(up to 8). Calls run in order, one at a time. A failed item is reported in its
own section and does not stop the rest. The batch fails only if every item fails.

## Progress and cancellation

- SuperCli sends `_meta.progressToken` with every `tools/call`. The server may
  emit `notifications/progress` (`progress`, optional `total`, optional
  `message`). Progress shows live on the running tool card (GUI) and in the
  status line (TUI). It never reaches the model.
- Server notifications and server→client requests may arrive interleaved
  with responses. `ping` gets an answer; other server requests get "method
  not found".
- When the user presses Stop, SuperCli sends
  `notifications/cancelled {requestId, reason}` and stops waiting immediately.

## Sessions

The server process stays alive across a conversation's calls (lazy start,
leases, and idle shutdown). With `session = "conversation"`, each `tools/call`
carries `_meta["supercli/session_id"]` so a server can keep per-conversation
state.
