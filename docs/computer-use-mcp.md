# Computer use through an explicitly installed MCP server

SuperCli uses configured MCP servers for mouse/keyboard/browser operations. It does not install a desktop server, grant OS accessibility rights, or silently start a computer-control service. The existing screenshot tool captures the screen or clipboard; capture alone is not mouse/keyboard automation.

Install and review the server yourself, including its OS permissions. Then configure its actual command and exact tool names in your portable `supercli-data/config.toml`:

```toml
[mcp.servers.desktop]
command = "/absolute/path/to/your-reviewed-mcp-server"
args = []
allowed_tools = ["take_screenshot", "click", "type_text"]
```

The names above are examples: use the names your installed server advertises. `mcp_bridge` list/search discovers configured capabilities lazily. No subprocess runs merely because the bridge was registered.

- `allowed_tools` is an exact allowlist enforced at the shared call boundary, including directly registered MCP tools. Excluded tools are not discoverable. An omitted list preserves existing installations; set one for desktop servers.
- MCP calls run without a per-action confirmation dialog. `allowed_tools` is the boundary; an old `confirm_calls` key is ignored.
- Native PNG/JPEG/GIF/WebP MCP image blocks now reach model vision and GUI preview without embedding base64 in text. Images are bounded, saved once in the portable session store, and re-armed for one provider request. The existing `load_session_image` mechanism can retrieve pixels later.
- The TUI prints the saved image location. Terminal inline image protocols are not assumed. The GUI supplies a native image card that opens a full preview.

Only configure a trusted executable. Starting an arbitrary server process can itself have side effects; SuperCli is not an OS-level sandbox for MCP processes. No user's computer was operated by the implementation tests.
