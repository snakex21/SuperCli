# demo MCP server

A tiny standalone MCP server (Go standard library only, stdio, newline-delimited
JSON-RPC, protocol `2025-06-18`) for testing SuperCli's MCP 2 features by hand.

## Build and install

From the repository root:

```
go build -o demo-mcp.exe ./examples/mcp/demo
```

Then copy `demo-mcp.exe` and `examples/mcp/demo/manifest.toml` into
`<supercli-data>/mcp/demo/`. Nothing else needs enabling: portable packages are
enabled unless the manifest says `enabled = false`. Start a new conversation
(or reload MCP) so SuperCli rediscovers packages.

On Linux/macOS build without `.exe` (`-o demo-mcp`); the manifest's
`command = "demo-mcp"` works on every OS.

## Tools

| Tool | Tests |
|---|---|
| `demo_image` | Image content (`image/png`) → preview / model image; read-only. |
| `demo_slow` | `seconds` (default 5, max 30). `notifications/progress` every second ("krok i/n"); Stop sends `notifications/cancelled` and the task ends. |
| `demo_big` | ~200 KB of text → truncated preview + `read_output` handle. |
| `demo_session` | Shows `_meta["supercli/session_id"]` and a per-session counter (manifest `session = "conversation"`). |
| `demo_write` | Echo only, but `destructiveHint=true` / `read_only=false` → treated as not read-only (confirmation). |

All tools are declared in `manifest.toml`, so `mcp_bridge` search lists them
without starting the process.
