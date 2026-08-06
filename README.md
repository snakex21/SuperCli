# SuperCli

Portable, sandboxed, single-binary AI CLI coding agent written in Go.

> **Beta / lab build:** SuperCli is usable, but still experimental. Not every
> workflow has been battle-tested yet; features are being checked, hardened, and
> polished as the project evolves. If you run it today, congratulations: you are
> officially a friendly lab mouse 🐭🧪. Please expect sharp edges, keep backups,
> and report anything weird.

SuperCli uses Bubble Tea for the TUI and `modernc.org/sqlite` for persistence,
so the default build is a standalone executable: no Node, no Python, no Docker,
no CGO runtime.

## Documentation

Index: **[docs/README.md](docs/README.md)** (reading order).

Start with [docs/quickstart.md](docs/quickstart.md), then
[data layout](docs/data-layout.md) and [architecture](docs/architecture.md).
Deeper: [delegation](docs/delegation.md) · [configuration](docs/configuration.md) ·
[performance](docs/performance.md) · [webgui](docs/webgui.md) ·
[project structure](docs/project-structure.md) · [PLAN](docs/PLAN.md) /
[ROADMAP](docs/ROADMAP.md). Docs explain *how* and *why* — decisions cite
measurements where it matters.

### Build (Windows)

| Script | Output |
|--------|--------|
| `build.bat` | `supercli.exe` (TUI / CLI) |
| `build_ui.bat` | `supercli-web.exe` (Web GUI, no console) |
| `run.bat` | build if needed, then run with `SUPERCLI_HOME=.` |

Local `*.exe` and `*.db` stay gitignored; keep them next to the scripts if you like.

## Why

Most CLI AI agents drift toward hidden global state, provider lock-in, large
runtime stacks, and IDE-like weight. SuperCli is built around the opposite
constraints:

- **Single-binary core** — `go build` produces one `supercli.exe` / `supercli`; the optional 1,410-skill content pack is shared from `supercli-data/skills/builtin-skills.zip`.
- **Always portable** — all data lives in a single `supercli-data/`
  directory next to the executable. Copy the folder, take your config,
  memory, sessions and credentials with you.
- **No user-profile writes** — nothing goes to `%APPDATA%`, `~/.config`,
  or `~/.supercli` (legacy `~/.supercli` data is migrated automatically
  on first start; the original is kept with a `MOVED.txt` marker).
- **Pure Go runtime** — no Node/Python/Docker dependency for normal operation.
- **Provider-flexible** — native Anthropic, OpenAI-compatible providers,
  ChatGPT/Codex OAuth, opencode gateway, echo mode, and configurable provider
  lists.
- **Test-driven** — `go test ./...` is expected to pass before a build is done.

## Quick start

```bash
go build -o supercli ./cmd/supercli
./supercli
```

Windows:

```powershell
go build -o supercli.exe ./cmd/supercli
.\supercli.exe
```

Open a different workspace without changing this copy's settings:

```bash
./supercli --home /tmp/my-project
SUPERCLI_HOME=/tmp/my-project ./supercli
```

Override the instance data directory only when explicitly needed:

```bash
./supercli --data-dir /tmp/supercli-instance-data
SUPERCLI_DATA_DIR=/tmp/supercli-instance-data ./supercli
```

Run a quick diagnosis:

```bash
./supercli --doctor
```

Run one prompt without the TUI:

```bash
./supercli --batch "summarize this project"
```

Maintainer quality and performance checks (separate tools; no runtime cost in
the normal binary):

```bash
go run ./cmd/supercli-eval --validate
go run ./cmd/supercli-eval --model local-qwen --model cloud-model -- \
  ./supercli --home {workspace} --model {model} --batch {prompt}

go build -o supercli ./cmd/supercli
go run ./cmd/supercli-perf --binary ./supercli --output test/perf/latest.json
```

## Core features

### TUI

- Bubble Tea terminal UI.
- Warm Claude-Code-inspired visual style.
- Structured chat transcript with separate `You` and `SuperCli` blocks.
- Responsive two-column/stacked welcome surface, compact short-terminal mode,
  and a focused bordered composer.
- Streaming assistant output.
- Status footer for credits, goals, tokens, and cost projection.
- Scrollable transcript with PgUp/PgDn.
- Cached completed transcript rendering, so streaming cost depends on the new
  delta instead of the full conversation length.
- `Ctrl+F` transcript search with per-message folding; folding is visual only
  and never changes the model context.
- Markdown rendering for assistant messages.
- Collapsible thinking blocks with `Shift+T`.
- Expand/collapse tool output with `Shift+E`.
- Compact tool activity rows (safe argument summaries and four-line previews by default).
- Command palette for `/` commands.
- GUI-like action centre on `Tab` / `Ctrl+K`, with common tasks and a
  searchable recent-session picker that do not require command names.
- Prompts typed while a run is active are queued for the next safe model step
  instead of being discarded or splitting a tool call from its result.
- `@file` mention autocomplete.
- Modal `/doctor` diagnostics screen.
- Height-bounded, virtualized menus for models, providers, settings, projects,
  accounts, and goal tasks; long local-model names never widen the terminal.

### Agent loop

- Native Anthropic Messages API provider.
- OpenAI-compatible streaming provider interface.
- Echo provider for offline tests and dry runs.
- opencode-compatible provider gateway support.
- Tool calling with JSON schema descriptions.
- Tool result verification hooks.
- Bounded agent loop steps.
- Session writing for user, assistant, tool calls, and tool results.
- Error attribution and tool error logging.
- Pattern reflection / learned pattern injection.
- Draft model support for token/cost savings.
- Cross-model consultation/council support.
- Darwin mode for parallel candidate generation and judging.
- Sub-agent registry and `agent` tool support.
- Ultrawork gates for goal/credit-driven execution loops.

### Tools

Built-in tool system includes:

- `tool_search` — searchable tool discovery.
- `apply_skill` — load and apply discovered skills.
- `ask_user` — ask the user structured questions from the TUI.
- `read_image` — attach/read images when the active model supports vision.
- `send_screenshot` — send clipboard/screenshot image input when available.
- `search_code` — search project code.
- `search_history` — search persisted session history.
- `read_lines` — read targeted line ranges.
- `read_context` — read surrounding context around a location.
- `edit_line` — edit one line.
- `insert_after` — insert text after a line.
- `delete_lines` — delete line ranges.
- `read_zip` — inspect ZIP archives.
- `read_docx` — extract current DOCX body/table/header/footer text, review comments and stable paragraph selectors using pure Go ZIP/XML parsing.
- `edit_docx` — style-preserving paragraph/table-cell edits, minimal tracked suggestions, review comments, header/footer replacement, previews and atomic backup writes.
- `read_xlsx` — extract XLSX sheets as text tables using pure Go ZIP/XML.
- `read_pdf` — extract PDF text via pure Go library integration.
- `ctx_execute` — run bounded context-mode scripts for large-file inspection.
- `goal` — expose active goal/tasks to the model.
- `darwin` — run parallel candidates and judge them.
- `consult` — sample/judge across available models.
- `hide_messages` — hide old messages from model context while preserving TUI scrollback.
- `check_library_alternatives` — suggest library replacements from a curated catalog.
- User-defined tools loaded from project config.

### Slash commands

Type `/` in the TUI to open the command palette.

| Command | Description |
| --- | --- |
| `/help` | Show command help. |
| `/goal <set\|list\|show\|tasks\|done>` | Manage the active goal and tasks. |
| `/darwin [N] <prompt>` | Run parallel agents and pick the best result. |
| `/council [<prompt>]` | No args: pick a roster of models (multi-select, saved in `[council]` of config.toml). With a prompt: ask the saved roster in parallel and show each answer plus a judge verdict. |
| `/clear` | Hide recent messages from the model context, keeping scrollback. |
| `/reflect` | Show learned patterns from reflection memory. |
| `/compact [instructions]` | Compress/hide context to save tokens. |
| `/status` | Show model, credit, and session status. |
| `/model [model_id]` | Pick or switch to one of the enabled models. |
| `/models` | Manage the complete model catalog, including disabled models. |
| `/providers` | Inspect, pause/resume, scan, and manage configured providers. |
| `/sandbox` | Show sandbox/home/data status. |
| `/plan` | Toggle read-only plan mode. |
| `/diff` | Show file changes recorded in the current session. |
| `/resume [session_id]` | List or resume previous sessions. |
| `/export [filename.md]` | Export current session to Markdown. |
| `/cost` | Show per-turn token/cost dashboard. |
| `/undo` | Conflict-safe revert of the last agent turn (does not touch your Git index/branch). |
| `/redo` | Restore the last reverted agent turn. |
| `/doctor` | Open runtime/config diagnostics modal. |
| `/quit` / `/exit` | Exit explicitly. |

### Sessions and persistence

- SQLite-backed session metadata and messages.
- Message sequence preservation.
- Resume support via `/resume`.
- Markdown export via `/export`.
- Searchable history through `search_history`.
- Single portable `supercli-data/` directory next to the binary.
- WAL mode for SQLite where used.

### Context and memory

- `@file` mention parsing and prompt prepending.
- Goal injection into system prompt.
- Context hiding/compaction.
- Reflection memory store for extracted patterns.
- Skill discovery and freshness reporting.
- Context-mode execution for bounded inspection of large files.

### Models and providers

- Native Anthropic API client.
- OpenAI-compatible API client.
- ChatGPT/Codex OAuth provider.
- opencode gateway provider.
- Echo provider.
- Provider list in `supercli-data/config.toml`.
- Model registry with capability metadata:
  - vision,
  - tool use,
  - streaming,
  - reasoning,
  - context length,
  - input/output costs.
- `/model` fast picker for enabled models and `/models` full visibility catalog with filtering and bulk toggles.
- `/providers` connection dashboard with model counts, pause/resume, scanning, and per-provider catalogs.
- `/settings` grouped editor, including the visible 60% prune threshold and the dynamic window-minus-reserve compaction policy.
- `--list-models`, `--refresh`, and `--model-info` non-interactive commands.
- Background provider model scanning.
- Hidden model state.

### Costs, credits, and stats

- Per-session and per-day token caps.
- Credit usage status bar.
- Audit log support.
- Per-turn stats recorder.
- Cost dashboard via `/cost`.
- External price fetching/cache with fallback rates.
- Draft model savings tracking.

### Diagnostics

- `--doctor` plain terminal diagnostics.
- `/doctor` modal TUI diagnostics.
- Checks include:
  - binary path,
  - Go runtime / OS / arch,
  - home directory,
  - data directory,
  - SQLite DB,
  - session store,
  - active provider,
  - provider config,
  - tool registry,
  - `git`,
  - optional `rg`.
- Long paths are truncated in the middle so the modal stays aligned.

### Sandbox and portability

- Data resolution is explicit and deterministic.
- All CLI state (config, databases, memory, sessions, OAuth tokens,
  caches, logs) is written to `supercli-data/` next to the executable.
- Data directory writeability is checked on startup; a read-only exe
  location (e.g. Program Files) produces a clear error with instructions
  instead of a crash.
- Shell escape runner is scoped to the configured home.
- File operations use project-relative/home-scoped paths; per-project
  workspace artifacts (project `config.toml` override, trash, snapshots)
  stay under `<project>/.supercli/`.
- Crash logs are written under `supercli-data/logs/`.

### Non-interactive modes

- `--status` — print credit usage and audit info.
- `--doctor` — run diagnostics and exit.
- `--batch "prompt"` — run one prompt without the TUI.
- `--list-models` — print known model capabilities.
- `--list-models --refresh` — refresh models from provider before printing.
- `--model-info ID` — print detailed metadata for one model.
- `--version` — print version.

## Configuration

### Data directory resolution (portable)

| Priority | Source | Data directory |
| --- | --- | --- |
| 1 | `--data-dir` flag | the explicitly selected directory |
| 2 | `$SUPERCLI_DATA_DIR` | the explicitly selected directory |
| 3 | default (portable) | `supercli-data/` next to this executable |

The resolved path is made absolute. The executable path is deliberately not
resolved through symlinks: each copied executable or launcher owns the
`supercli-data/` directory beside itself. `--home` and `SUPERCLI_HOME` select
only the workspace/sandbox and never redirect settings, sessions, memory,
auth, logs, or caches to another SuperCli copy.

For the terminal CLI, on first start existing data in the legacy
`~/.supercli` location may be copied into an empty adjacent `supercli-data/`;
after that every instance remains independent. The original is left in place.

The project working directory (cwd) is still used for project-scoped
artifacts: a `.supercli/config.toml` override, trash, and snapshots.

### Environment variables

```text
SUPERCLI_HOME
SUPERCLI_DATA_DIR
SUPERCLI_LLM_PROVIDER
SUPERCLI_LLM_API_KEY
SUPERCLI_LLM_BASE_URL
SUPERCLI_LLM_MODEL
SUPERCLI_LLM_TEMPERATURE
SUPERCLI_LLM_STREAM
SUPERCLI_LLM_TIMEOUT
SUPERCLI_DEBUG
```

### CLI flags

```text
--home PATH
--data-dir PATH
--provider P
--model M
--key K
--base-url U
--echo
--debug
--status
--doctor
--list-models
--refresh
--model-info ID
--max-credits-per-session N
--max-credits-per-day N
--draft-mode off|always|balanced|critical   # default off; opt-in
--draft-model ID                            # required to enable F11 (no auto-pick)
--config PATH
--batch PROMPT
--version
```

### TOML config

SuperCli reads config from `supercli-data/config.toml` (global) and `<project>/.supercli/config.toml` (override) layers. Supported fields include:

```toml
default_model = "gpt-4o-mini"
default_provider = "local"
draft_mode = "off"   # opt-in; F11 stays off unless draft_model is also set
draft_model = ""     # set a model id to enable F11 speculative drafting
max_credits_per_session = 0
max_credits_per_day = 0
no_color = false
provider = "openai"
max_steps = 0     # 0 = built-in runaway safety net (300); not a work budget
debug = false

[[providers]]
name = "local"
type = "openai"
base_url = "http://localhost:1234/v1"
api_key = "lm-studio"
model = "qwen"

[[model_prices]]
model = "qwen"
input_cost = 0.15
output_cost = 0.60
```

## Keyboard shortcuts

| Key | Action |
| --- | --- |
| `Enter` | Send prompt / accept modal action. |
| `Tab` on empty input / `Ctrl+K` | Open the action centre (models, sessions, projects, goal, files, usage, settings). |
| `Ctrl+F` | Search the current transcript and fold/unfold one matching block. |
| `/` | Open command palette. |
| `@` | Open file mention autocomplete. |
| `Esc` | Clear input, close autocomplete/modal, or cancel run. |
| `Ctrl+C` | Interrupt current run or quit. |
| `PgUp` / `PgDn` | Scroll transcript. |
| `Shift+T` | Toggle thinking block visibility. |
| `Shift+E` | Expand/collapse tool output. |
| `Tab` | Insert selected autocomplete item. |
| `↑` / `↓` | Navigate autocomplete and menus. |
| `q` | Close modal views such as `/doctor`. |
| `r` | Refresh `/doctor` modal. |

## Project layout

```text
SuperCli/
  cmd/supercli/            # binary entrypoint
  internal/
    account/               # auth, credits, pricing, tiers
    agent/                 # agent loop plus darwin/reflect/ultrawork subpackages
    app/                   # startup, flags, provider/tool/TUI wiring
    llm/                   # provider interfaces; Anthropic/OpenAI/Codex/opencode/echo
    storage/               # sessions, memory, goals, library
    system/                # config, doctor, stats
    tools/                 # tool facade plus domain packages (files, office, web, ...)
    ui/                    # TUI and export
  test/                    # integration/stress/benchmark tests
  Makefile
  build.bat
  run.bat
```

## Testing and build

```bash
go test ./...
go build -o supercli ./cmd/supercli
```

Windows:

```powershell
go test ./...
go build -o supercli.exe ./cmd/supercli
```

## Current status

SuperCli is an active local-first AI CLI prototype. The codebase already includes
the real TUI, provider wiring, tool registry, persistence, model/provider menus,
diagnostics, cost tracking, session export/resume, and many built-in tools.

Areas still evolving include permissions UX, checkpoint/rewind flows, deeper MCP
management screens, and richer transcript/history search.

## License

TBD.
