# Project structure

SuperCli is organized by runtime responsibility, not by interface. The TUI,
WebGUI and batch mode share the same agent, provider, tool and storage layers.
This keeps fixes in the execution path independent from the surface that
started a run.

```text
cmd/                    thin executable entry points
  supercli/             TUI and batch binary
  supercli-web/         desktop/WebGUI binary and Windows resources
  supercli-eval/        maintainer-only model quality matrix
  supercli-perf/        maintainer-only startup/RAM smoke

internal/
  app/                  composition root and CLI command wiring
  agent/                model/tool loop, routing and context defense
  llm/                  provider protocol, clients, metering and failover
  tools/<domain>/       bounded capabilities exposed to the agent
  storage/<domain>/     SQLite and durable state services
  system/<domain>/      configuration and host/runtime services
  account/              authentication, limits, prices and credits
  codeintel/            optional language intelligence
  ui/tui/               terminal presentation and input handling
  webgui/               HTTP/SSE handlers and desktop web presentation
  verification/         deterministic verification services
  agenteval/            eval implementation; not linked into normal CLI
  perfbench/             performance smoke; not linked into normal CLI

test/                   cross-package integration, replay and eval fixtures
docs/                   operator and architecture documentation
supercli-data/          portable runtime assets only; no source code
go.mod, go.sum          Go module manifest and checksums; must stay at module root
```

## Dependency direction

The intended direction is:

```text
cmd -> app/webgui -> agent -> llm + tools -> storage/system
                     ui consumes agent events
```

`internal/app` is allowed to know every runtime subsystem because it is the
composition root. Domain packages must not import `app`, TUI and WebGUI must
not import each other, and ordinary runtime packages must not import
`agenteval` or `perfbench`.

## File placement rules

- Executable `main.go` files only parse process-level arguments and call an
  application entry point.
- Provider construction and routing live in `internal/llm`; UI code only
  selects configuration.
- Agent-visible operations live in `internal/tools/<domain>`. Shared result,
  error and output-cap contracts stay in `internal/tools/core`.
- Durable data access lives in `internal/storage/<domain>` and is exposed as a
  service instead of being opened independently by every handler.
- Platform-specific code uses `_windows.go`, `_linux.go` and `_other.go`
  siblings rather than runtime branches spread through feature files.
- Test fixtures and maintainer benchmarks never enter `supercli-data/` and are
  never embedded in the release binary.
- `go.mod` and `go.sum` stay beside the module source root. Moving them into a
  cosmetic subdirectory would change the module boundary and break ordinary
  `go build`, `go test` and editor/package discovery.
- Databases, logs, credentials, generated executables and temporary files are
  runtime state, not source. Portable builds keep that state in
  `supercli-data/`; repository-root copies are ignored and may be removed once
  the application is closed and their data is no longer needed.

## Refactoring policy

Large files are split inside their current package first. A new package is
created only when it establishes a real dependency boundary; moving code into
many tiny packages merely to shorten files makes navigation and compilation
harder.

Current extraction order:

1. `internal/app/main.go`: runtime configuration is already in
   `runtime_config.go`; next candidates are provider construction, goal/slash
   commands and diagnostics.
2. `internal/agent/loop.go`: stream/tool-call parsing, persistence telemetry
   and draft verification can become same-package files.
3. `internal/ui/tui/model.go`: input/ask handling and transcript rendering can
   be separated without changing the Bubble Tea model type.

Every extraction must be behavior-neutral, pass `go test ./...`, and avoid
changing prompt bytes, tool schemas or persisted formats.
