# SuperCli — quickstart and map

Read this first. It tells you what SuperCli is optimizing for and where
everything lives; the other docs go one level deeper.

- [architecture.md](architecture.md) — the agent loop end-to-end (routing,
  thin tools, KV-cache discipline, compaction)
- [delegation.md](delegation.md) — task workers, orchestrator mode,
  model-per-task, draft-verify, second opinion
- [performance.md](performance.md) — telemetry, warm cache, preflight,
  noop-gate, turn economy
- [configuration.md](configuration.md) — every knob, its default, and when
  (not) to touch it
- [webgui.md](webgui.md) — web GUI design notes

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

Web GUI (Windows needs the GUI-subsystem flag, see PROJECT_SKILL.md):

```powershell
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
```

All state lives in `supercli-data/` next to the binary (portable; no
`~/.config`, no `%APPDATA%`). Config: `supercli-data/config.toml` global,
`<project>/.supercli/config.toml` per-project override.

## Map (7 domains under internal/)

```text
internal/
  account/   auth, credits, pricing, tiers
  agent/     THE loop: routing, thin tools, prune/compact, delegation,
             orchestrator, draft-verify; subpackages darwin/reflect/
             planmode/ultrawork
  app/       startup wiring: flags, config resolution, defaults contract,
             noop-gate, batch mode
  llm/       providers (Anthropic/OpenAI/Codex/Responses/opencode/echo),
             token estimator, system-message demote, slot cache,
             capabilities, effort
  storage/   sessions, memory, goals, library (SQLite)
  system/    config (TOML layers), doctor, manifest, preflight, stats
  tools/     tool registry facade + domain packages (files, office, web,
             search, sandbox, ...)
  ui/        TUI (Bubble Tea) and export
  webgui/    web GUI server + embedded assets
```

Sources of truth when docs and code disagree: the code and its tests.
Key contract tests worth knowing: `internal/app/defaults_test.go`
(fresh-install defaults), `internal/agent/cache_prefix_test.go`
(append-only prompt), `internal/llm/system_demote_test.go` (no system
hoisting).
