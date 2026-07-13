# Portable MCP packages

SuperCli can carry MCP bridges beside its own data. Copying the whole SuperCli
folder to another computer also copies these packages and their configuration.
No absolute path is stored in a package manifest.

Portable packages live here:

```text
supercli-data/
  mcp/
    blender/
      manifest.toml
      bin/
        blender-mcp.exe
    godot/
      manifest.toml
      runtime/
      server/
```

Each direct child of `supercli-data/mcp` is one package. Folders without a
`manifest.toml` are ignored. A package is discovered at startup, but its process
is **not** started. The model sees one small `mcp_bridge` tool; the selected MCP
server starts only when the model searches or calls it.

## Manifest

```toml
schema = 1
name = "blender"
version = "1.0.0"
description = "Inspect and edit the active Blender scene"
command = "bin/blender-mcp.exe"
args = ["--transport", "stdio"]
tags = ["3d", "scene", "render", "blender"]
platforms = ["windows/amd64"]

[[requires]]
name = "Blender"
candidates = [
  "blender",
  "${ProgramFiles}/Blender Foundation/Blender */blender.exe"
]
```

The bridge executable travels with SuperCli, while Blender itself may remain an
installed host application. If it is absent, the MCP panel reports `missing:
Blender` instead of failing the application.

A package that carries its own Node runtime can use:

```toml
schema = 1
name = "my-node-mcp"
description = "Example MCP with a bundled runtime"
command = "runtime/node.exe"
args = ["${MCP_DIR}/server/index.js"]
tags = ["example"]
```

Available placeholders:

| Placeholder | Resolves to |
|---|---|
| `${MCP_DIR}` | current package directory |
| `${DATA_DIR}` | `supercli-data` directory |
| `${SUPERCLI_DIR}` | directory containing SuperCli and `supercli-data` |
| `${OS}` / `${ARCH}` | current Go platform names |
| ordinary `${ENV_NAME}` | environment variable on the current computer |

Relative `command` and `cwd` paths are resolved from the package directory.
Use `${MCP_DIR}` for relative paths embedded in `args`.

## External dependencies and secrets

`[[requires]]` entries are diagnostics, not installers. Candidates can be an
executable name from `PATH`, an absolute/relative path, or a glob. Set
`optional = true` for capabilities that are helpful but not required.

Do not place API keys in a distributable `manifest.toml`. Put secrets in the
existing `[mcp.servers.<name>.env]` section of `supercli-data/config.toml`, or
reference an environment variable from the manifest. An explicit
`[mcp.servers.<name>]` entry overrides a portable package with the same name.

## Moving to another computer

1. Close SuperCli so active MCP child processes stop cleanly.
2. Copy the SuperCli directory including `supercli-data/mcp`.
3. Open the MCP panel. Packages are checked against the new computer and shown
   as ready, disabled, or unavailable with the missing dependency.
4. The first agent call starts only the package it actually uses.

Host applications such as Blender or Godot are not copied automatically. Their
MCP bridge and portable runtime can travel with SuperCli; the host must either
already exist on the destination computer or be included legally in the
package itself.
