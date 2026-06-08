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

## Why

Most CLI AI agents drift toward hidden global state, provider lock-in, large
runtime stacks, and IDE-like weight. SuperCli is built around the opposite
constraints:

- **Single binary** — `go build` produces one `supercli.exe` / `supercli`.
- **Portable by default** — state lives in `<home>/.supercli/`; by default
  `<home>` is the current working directory.
- **Project-local state** — no `%APPDATA%`, `~/.config`, or user home writes
  unless explicitly configured.
- **Pure Go runtime** — no Node/Python/Docker dependency for normal operation.
- **Provider-flexible** — OpenAI-compatible providers, opencode gateway, echo
  mode, and configurable provider lists.
- **Test-driven** — `go test ./...` is expected to pass before a build is done.

## Quick start

```bash
go build -o supercli .
./supercli
```

Windows:

```powershell
go build -o supercli.exe .
.\supercli.exe
```

Override the home directory:

```bash
./supercli --home /tmp/my-project
SUPERCLI_HOME=/tmp/my-project ./supercli
```

Run a quick diagnosis:

```bash
./supercli --doctor
```

Run one prompt without the TUI:

```bash
./supercli --batch "summarize this project"
```

## Core features

### TUI

- Bubble Tea terminal UI.
- Warm Claude-Code-inspired visual style.
- Structured chat transcript with separate `You` and `SuperCli` blocks.
- Streaming assistant output.
- Status footer for credits, goals, tokens, and cost projection.
- Scrollable transcript with PgUp/PgDn.
- Markdown rendering for assistant messages.
- Collapsible thinking blocks with `Shift+T`.
- Expand/collapse tool output with `Shift+E`.
- Command palette for `/` commands.
- `@file` mention autocomplete.
- Modal `/doctor` diagnostics screen.
- Interactive menus for models, providers, and goal tasks.

### Agent loop

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
- `read_docx` — extract DOCX text using pure Go ZIP/XML parsing.
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
| `/council [N] <prompt>` | Sample multiple models and let a judge pick the winner. |
| `/clear` | Hide recent messages from the model context, keeping scrollback. |
| `/reflect` | Show learned patterns from reflection memory. |
| `/compact [instructions]` | Compress/hide context to save tokens. |
| `/status` | Show model, credit, and session status. |
| `/models` | Open/list known models and capabilities. |
| `/model [model_id]` | Show or switch active model. |
| `/providers` | Manage configured providers. |
| `/sandbox` | Show sandbox/home/data status. |
| `/plan` | Toggle read-only plan mode. |
| `/diff` | Show file changes recorded in the current session. |
| `/resume [session_id]` | List or resume previous sessions. |
| `/export [filename.md]` | Export current session to Markdown. |
| `/cost` | Show per-turn token/cost dashboard. |
| `/undo [N]` | Revert last file write/edit operations tracked by SuperCli. |
| `/doctor` | Open runtime/config diagnostics modal. |
| `/quit` / `/exit` | Exit explicitly. |

### Sessions and persistence

- SQLite-backed session metadata and messages.
- Message sequence preservation.
- Resume support via `/resume`.
- Markdown export via `/export`.
- Searchable history through `search_history`.
- Per-project `.supercli/` data directory.
- WAL mode for SQLite where used.

### Context and memory

- `@file` mention parsing and prompt prepending.
- Goal injection into system prompt.
- Context hiding/compaction.
- Reflection memory store for extracted patterns.
- Skill discovery and freshness reporting.
- Context-mode execution for bounded inspection of large files.

### Models and providers

- OpenAI-compatible API client.
- opencode gateway provider.
- Echo provider.
- Provider list in `.supercli/config.toml`.
- Model registry with capability metadata:
  - vision,
  - tool use,
  - streaming,
  - reasoning,
  - context length,
  - input/output costs.
- `/models`, `/model`, and `/providers` TUI menus.
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

- Home resolution is explicit and deterministic.
- Writes are scoped to `<home>/.supercli/`.
- Data directory writeability is checked on startup.
- Shell escape runner is scoped to the configured home.
- File operations use project-relative/home-scoped paths.
- Crash logs are written under `.supercli/logs/`.

### Non-interactive modes

- `--status` — print credit usage and audit info.
- `--doctor` — run diagnostics and exit.
- `--batch "prompt"` — run one prompt without the TUI.
- `--list-models` — print known model capabilities.
- `--list-models --refresh` — refresh models from provider before printing.
- `--model-info ID` — print detailed metadata for one model.
- `--version` — print version.

## Configuration

### Home resolution

| Priority | Source | Example |
| --- | --- | --- |
| 1 | `--home` flag | `supercli --home /tmp/sandbox` |
| 2 | `$SUPERCLI_HOME` | `SUPERCLI_HOME=/tmp/sandbox supercli` |
| 3 | current directory | `cd /tmp/sandbox && supercli` |

The resolved path is made absolute. Runtime state is stored under:

```text
<home>/.supercli/
```

### Environment variables

```text
SUPERCLI_HOME
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
--draft-mode off|always|balanced|critical
--draft-model ID
--config PATH
--batch PROMPT
--version
```

### TOML config

SuperCli reads config from `.supercli/config.toml` layers. Supported fields include:

```toml
default_model = "gpt-4o-mini"
default_provider = "local"
draft_mode = "critical"
draft_model = ""
max_credits_per_session = 0
max_credits_per_day = 0
no_color = false
provider = "openai"
max_steps = 10
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
  main.go                  # startup, flags, provider/tool/TUI wiring
  internal/
    agent/                 # agent loop, events, sub-agents, session writer integration
    config/                # env/TOML/flag configuration
    consult/               # multi-model consultation and judging
    cost/                  # TUI cost dashboard rendering
    credits/               # budgets, usage, audit, cost rates
    ctxexec/               # bounded context execution helper
    darwin/                # parallel candidate generation/judging
    doctor/                # runtime diagnostics
    draft/                 # draft model policy and savings
    export/                # Markdown session export
    fileops/               # tracked file edits and undo
    freshness/             # skill freshness checks
    goal/                  # active goal/tasks service
    library/               # library alternative catalog
    llm/                   # provider interfaces and OpenAI/opencode/echo clients
    mcp/                   # MCP-related plumbing
    memory/                # memory/pattern storage
    mentions/              # @file mention parsing/resolution
    planmode/              # read-only planning prompt wrapper
    pricing/               # external model price fetch/cache
    providers/             # provider manager and model scanning
    reflect/               # pattern extraction/injection
    sandbox/               # path/env/sandbox policy helpers
    session/               # SQLite sessions/messages
    shellescape/           # !command shell escape runner
    stats/                 # per-turn token/cost stats
    storage/               # home resolution and SQLite DB bootstrap
    tools/                 # built-in and user tool registry
    tui/                   # Bubble Tea UI, command palette, modals, menus
    ultrawork/             # goal/credit execution gates
  test/                    # integration/stress/benchmark tests
  Makefile
  build.bat
  run.bat
```

## Testing and build

```bash
go test ./...
go build -o supercli .
```

Windows:

```powershell
go test ./...
go build -o supercli.exe .
```

## Current status

SuperCli is an active local-first AI CLI prototype. The codebase already includes
the real TUI, provider wiring, tool registry, persistence, model/provider menus,
diagnostics, cost tracking, session export/resume, and many built-in tools.

Areas still evolving include permissions UX, checkpoint/rewind flows, deeper MCP
management screens, and richer transcript/history search.

## License

TBD.
