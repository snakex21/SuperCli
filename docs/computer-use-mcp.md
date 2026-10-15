# Computer use through an explicitly installed MCP server

SuperCli uses configured MCP servers for additional mouse/keyboard/browser operations. Native Windows background accessibility and process-window capture are documented in [Application window screenshots](app-window-capture.md). SuperCli does not install a desktop server, grant OS accessibility rights, or silently start a computer-control service.

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

## Reusable Windows backends reviewed on 2026-10-15

These are source reviews, not comparative runtime benchmarks. No external desktop server was installed or enabled by this review.

| Project | License / implementation | Relevant findings for SuperCli |
| --- | --- | --- |
| [wincrust](https://github.com/nhatvu148/wincrust) | MIT OR Apache-2.0; single Rust binary, seven MCP tools | A close architectural match: scoped discovery, semantic actions and compact text/image/diff observations. Its [Windows UIA implementation](https://github.com/nhatvu148/wincrust/blob/main/src/uia/win.rs) uses native patterns and cached discovery. The documented launch/remote-service setup uses LocalAppData and elevation; review/redirect those paths before a portable integration. It was source-reviewed here, not run or benchmarked. |
| [Terminator](https://github.com/mediar-ai/terminator) | MIT; Rust engine, with MCP and language bindings | [Cached tree building](https://github.com/mediar-ai/terminator/blob/main/crates/terminator/src/platforms/windows/tree_builder.rs) groups property reads. Its [Windows element implementation](https://github.com/mediar-ai/terminator/blob/main/crates/terminator/src/platforms/windows/element.rs) separates semantic `invoke`/`set_value` from physical `click` and focus-capable typing. A background integration must select the semantic operations explicitly. |
| [Microsoft winapp CLI](https://github.com/microsoft/winappCli/blob/main/docs/ui-automation.md) | MIT; C# | Documents scoped selectors, ambiguity failures, UIA invocation and post-action state. Physical click/key commands require a foreground desktop. Its documented workflow coordination folder is in the user profile; portability requires reviewing data paths before enabling it. |
| [Windows-MCP](https://github.com/CursorTouch/Windows-MCP) | MIT; Python 3.13+ and uv | Ready-made MCP server, but its [window-switch implementation](https://github.com/CursorTouch/Windows-MCP/blob/main/src/windows_mcp/desktop/service.py) restores/activates windows and waits for foreground focus. It is not a direct replacement for focus-preserving background actions. |
| [go-mcp-computer-use](https://github.com/coff33ninja/go-mcp-computer-use) | Apache-2.0; Go, CGO/ONNX components | Provides native UIA, but the [menu helper](https://github.com/coff33ninja/go-mcp-computer-use/blob/v0.3.x/internal/actions/chained.go) uses OCR and a physical click. The documented distribution includes ML models, native ONNX runtime and AppData storage; a portable integration needs more than copying its executable. |

For the built-in Go path, keep the execution surface small: inspect one process, perform a semantic action, return immediate observed state when supported. Serialize work by process creation identity, including capture and modal windows, rather than making unrelated background applications share a desktop-wide queue. Release idle gates instead of retaining an unbounded application cache. This does not change model reasoning settings or introduce another model call.

When evaluating an external backend, use a small exact `allowed_tools` list and the existing lazy MCP bridge. Verify its real input behavior and portable data paths from source; an accessibility tree or a background-mode claim does not establish that all its click/type commands avoid the physical desktop.
