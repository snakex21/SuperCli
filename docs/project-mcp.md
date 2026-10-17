# Project MCP servers and background runs

Use the existing project configuration file, `<project>/.supercli/config.toml`:

```toml
[mcp.servers.project-tools]
command = "my-mcp-server"
args = ["--stdio"]
allowed_tools = ["search", "read"]

[mcp.servers.project-tools.env]
WORKSPACE_TOKEN = "local-secret"
```

The format is the same as `<dataDir>/config.toml`. Global servers remain
available. A project server replaces the **whole server entry** with the same
name (including arguments, environment, and tool allow-list); other names are
added. Explicit TOML entries take precedence over global portable packages in
`<dataDir>/mcp`. Keep credentials out of version control. MCP commands execute
local programs: only use configuration from projects you trust.

The web GUI gives each project a separate MCP manager, including separate
instances of globally configured servers. Explicit commands run in that
project's directory; portable packages retain their manifest working directory.
Processes start only when the MCP bridge searches/calls a server. A foreground
run holds a reference through model calls and pending user questions. Individual
bridge operations also hold references. After the last reference, servers stop
after one minute idle and restart lazily on demand. Idle shutdown is postponed
while any detached worker is running, conservatively protecting inherited
bridges. Config reloads still wait until all foreground runs and workers finish.
The TUI already uses the same global/project TOML resolver; no second MCP file
format is introduced.

The Projects sidebar lists conversations running in other projects, with title,
project name, server-based elapsed time, and working/waiting-for-answer state.
Click a row to switch projects and attach through the existing live-sync path.
Switching does not stop the conversation you leave. Sync snapshots restore the
list after reconnect; only the selected conversation receives transcript events.
