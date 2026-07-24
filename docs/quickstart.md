# SuperCli — quickstart and map

**Start here.** SuperCli optimizes for portable local-agent work: one binary,
data next to the exe, cheap turns on llama.cpp-class servers, one engine behind
TUI / WebGUI / batch.

## Reading order (about 30–40 minutes)

| # | Doc | Why |
|---|-----|-----|
| 1 | **This file** | Vision in one page + where to run |
| 2 | [data-layout.md](data-layout.md) | HOME vs `supercli-data` vs project `.supercli/` |
| 3 | [architecture.md](architecture.md) | One user turn: route → cache → tools → answer |
| 4 | [configuration.md](configuration.md) | Empty config is best; knobs as escapes |
| 5 | [delegation.md](delegation.md) | Workers, orchestrator, draft-verify |

**When you need more (not required on day one):**

| Doc | When |
|------|------|
| [performance.md](performance.md) | Deep telemetry, KV/cache numbers, turn economy |
| [webgui.md](webgui.md) | Desktop / web UI design |
| [project-structure.md](project-structure.md) | Source tree + file-name prefixes after refactor |
| [PROJECT_SKILL.md](PROJECT_SKILL.md) | Build rules for agents editing this repo |
| [PLAN.md](PLAN.md) | Near-term checklist |
| [ROADMAP.md](ROADMAP.md) | What is shipped vs backlog (long; product registry) |
| [portable-mcp.md](portable-mcp.md) · [builtin-skills.md](builtin-skills.md) · [telemetry.md](telemetry.md) | Feature-specific |

## What

A portable, sandboxed, single-binary AI CLI coding agent in Go (Bubble Tea
TUI + a separate web GUI binary). Built primarily for **local models**
(llama.cpp-family servers), where every prompt token is paid in wall-clock
prefill time on the user's own GPU — but every optimization must also work
against cloud endpoints (they are simply never probed with local-only
features).

## Why (the three design laws)

1. **"It just works": an empty config must be the best config.** Every
   default is the measured-best choice for the typical setup (one local
   model, one host). Knobs exist as escape hatches, not as required setup.
   This contract is pinned by a test: `internal/app/defaults_test.go` —
   changing a default means editing that test, i.e. a conscious decision.
2. **Token minimalism, KV-cache first.** On a local server, re-evaluating
   a prompt prefix is the dominant cost. The prompt is therefore kept
   **byte-stable at the front and append-only at the tail**, everywhere:
   tool schemas, system text, history edits. Anything that would rewrite
   the prompt front is either removed, demoted to the tail, or batched
   (see architecture.md for the measured numbers behind each rule).
3. **The turn is the most expensive unit.** A saved round-trip beats a
   saved token. Features like the repo preflight block (~73 tokens buying
   −33% turns) exist because one avoided discovery turn dwarfs its cost.

## Run

```bash
go build -o supercli ./cmd/supercli
./supercli                     # TUI
./supercli --batch "prompt"    # one prompt, no TUI
./supercli --doctor            # diagnostics
```

Web GUI (Windows needs the GUI-subsystem flag, see [PROJECT_SKILL.md](PROJECT_SKILL.md)):

```powershell
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
```

### Where data lives (two roots)

| Root | Meaning |
|------|---------|
| **HOME** (`--home` / `SUPERCLI_HOME` / cwd) | Workspace the agent edits |
| **DATA DIR** (`supercli-data/` next to the exe by default) | App state: chats, auth, global config |
| **`<HOME>/.supercli/`** | Optional **project** config overlay (e.g. `config.toml`) |

If you run SuperCli *inside* a project folder, `.supercli/` there is that
project overlay — not “junk”. Full diagrams: [data-layout.md](data-layout.md).

Global app config: `supercli-data/config.toml`. Portable; no `%APPDATA%` /
`~/.config` required. Legacy `~/.supercli` may be migrated once into
`supercli-data/`.

## Map (domains under internal/)

```text
internal/
  account/   auth, credits, pricing, tiers
  agent/     THE loop: routing, thin tools, prune/compact, delegation,
             orchestrator, draft-verify; subpackages darwin/reflect/
             planmode/ultrawork
  app/       composition root: flags → workspace → wire → TUI/batch
  llm/       providers, metering, capabilities, slot cache, effort
  storage/   sessions, memory, goals, library (SQLite); home resolution
  system/    config (global + project TOML), doctor, preflight, stats
  tools/     tool registry + domain packages (files, office, web, …)
  ui/tui/    Bubble Tea presentation
  webgui/    HTTP/SSE + desktop web UI
```

Sources of truth when docs and code disagree: the code and its tests.
Key contract tests worth knowing: `internal/app/defaults_test.go`
(fresh-install defaults), `internal/agent/cache_prefix_test.go`
(append-only prompt), `internal/llm/system_demote_test.go` (no system
hoisting).
