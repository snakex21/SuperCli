# Project structure

SuperCli is organized by runtime responsibility, not by interface. The TUI,
WebGUI and batch mode share the same agent, provider, tool and storage layers.
This keeps fixes in the execution path independent from the surface that
started a run.

```text
# Repo root — intentionally thin
#
# MUST stay at module root (Go / open-source convention):
#   go.mod, go.sum     Go module identity (cannot move without breaking builds)
#   README.md, LICENSE, THIRD_PARTY_NOTICES.md
#   Makefile
#
# Convenience entrypoints (Windows) — stay at root by design:
#   build.bat, build_ui.bat, run.bat
#   supercli.exe, supercli-web.exe   local only; gitignored
#
# NEVER commit / keep as source:
#   memory.db, sessions.db in root   orphan runtime junk (use supercli-data/)
#   plans/screenshots                live under docs/

cmd/                    thin executable entry points
internal/               all application source
docs/                   operator + architecture docs, plans, screenshots
scripts/                maintainer one-shots (splitters, helpers)
test/                   cross-package integration, replay and eval fixtures
supercli-data/          portable *application* data next to the binary
                        (mostly gitignored; not the same as project .supercli/)
```

**Runtime paths (not source):** see [data-layout.md](./data-layout.md).

| Path | Role |
|------|------|
| `supercli-data/` next to exe | Global app state (config, sessions, auth, memory DBs) |
| `<workspace>/.supercli/` | Optional per-project config override |
| Repo checkout `.supercli/` when `HOME` is the repo | Same project overlay — normal when developing SuperCli *in* SuperCli |

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

### Same-package file prefixes

Go requires one package name per directory. Large packages stay one package;
files use **subject prefixes** so directories stay scannable without inventing
dozens of public packages.

#### `internal/app` (`package app`)

| Prefix | Role |
|--------|------|
| `main.go` | composition root (pipeline only) |
| `startup_*` | flags, workspace, config, onboarding, portable paths |
| `wire_*` | tools, loop, TUI options, slash, council, draft, providers |
| `memory_*` | dual memory store, idle saver, `/memory` |
| `provider_*` | provider construction, codex pool, tiers |
| `runtime_*` | session store, status bar, pricing, shutdown, env helpers |
| `cmd_*` | slash/batch/doctor/projects/resume/mcp/workers |
| `platform_*` | crash log, app log, Windows close handling |
| `adapter_*` | bridges into ultrawork / goal / noopgate |
| `state_*` | process-level globals (prompt, coordinator flags) |
| `util_*` | small shared helpers |

#### `internal/agent` (`package agent`)

| Prefix | Role |
|--------|------|
| `core.go` | Agent interface surface |
| `loop_*` / `loop.go` | run loop, steps, route, persist, tools, stats |
| `context_*` | compact, prune, hide, summarize, report, token budget |
| `tool_*` | invoke, send_message, task_stop, sentinel, repair |
| `stream_*` | SSE consume, strip thinking |
| `window_*` | context-window estimate/compact |
| `worker_*` | worker registry |
| `agent_tool_*` | coordinator task tool |
| `prompt_*` | coordinator / orchestrator prompt text |
| `route_*` | keyword route map |
| `subagent_*` | sub-agent registry + builtins |
| `draftverify_*` | draft-verify ladder |

Subpackages already exist where they are real boundaries: `darwin/`,
`planmode/`, `reflect/`, `ultrawork/`.

#### `internal/webgui` (`package webgui`)

| Prefix | Role |
|--------|------|
| `http_*` | server, router, handlers, remote auth |
| `engine_*` | agent engine, loop, prompt, workers, workspace |
| `stream_*` / `run_*` | SSE stream, chat, question, checkpoint, telemetry |
| `data_*` | sessions, backup/export/import, transcript, memory/goal |
| `ctl_*` | models, providers, codex, reasoning, config knobs |
| `folder_*` | folder indexing and document extract |
| `desktop_*` | native window, taskbar, signals, open-folder (OS build tags) |
| `feat_*` | attachments, files, MCP, projects, stats, settings, … |

#### `internal/ui/tui` (`package tui`)

| Prefix | Role |
|--------|------|
| `model_*` | Bubble Tea model: input, events, slash, shell, cancel |
| `menu_*` | interactive menus (models, providers, settings, …) |
| `view_*` | chat, markdown, markers, theme, status, scroll, i18n |
| `actions_*` / `autocomplete_*` / `onboard_*` | feature clusters |
| `cmd_*` | help, doctor, slash parse/handlers |

#### `internal/llm` (`package llm`)

| Prefix | Role |
|--------|------|
| `openai_*` / `anthropic_*` / `codex_*` / … | transport clients |
| `cap_*` | capabilities, catalog, provider list, probe |
| `proto_*` | messages, deltas, SSE, tool schema |
| `meter_*` | metering, tokens, estimates, slot cache |
| `net_*` | HTTP timeouts, retry, idle reader |
| `model_*` | reasoning effort, thinking soft-switch, system demote |

Subpackages: `consult/`, `draft/`, `factory/`, `prompt/`, `providers/`, `shuffler/`.

#### `internal/storage/{memory,session}` 

| Prefix | Role |
|--------|------|
| `store_*` | open store, query, migrate, encode, writer |
| `feature_*` | autosave, briefing, hybrid search, tasks, … |
| `platform_*` | OS-specific file replace (memory) |

#### `internal/tools/{core,office,…}`

| Area | Convention |
|------|------------|
| `core` | `registry_*`, `output_*`, `schema_*`, `args_*` |
| `office` | `read_*`, `edit_*`, `office_*` |
| `files` / `fileops` | one file per tool name (already clear) |

Prefer `prefix_topic.go` over many public subpackages. Nested
`internal/<pkg>/internal/...` packages are an option only when a real API
boundary is needed.

Every extraction must be behavior-neutral, pass `go test ./...`, and avoid
changing prompt bytes, tool schemas or persisted formats.
