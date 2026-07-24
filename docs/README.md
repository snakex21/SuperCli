# SuperCli documentation

## Start here

1. **[quickstart.md](./quickstart.md)** — vision, run commands, reading order  
2. **[data-layout.md](./data-layout.md)** — workspace vs `supercli-data` vs project `.supercli/`  
3. **[architecture.md](./architecture.md)** — one agent turn end-to-end  

## Core design

| Doc | Topic |
|------|--------|
| [configuration.md](./configuration.md) | Knobs; empty config is best |
| [delegation.md](./delegation.md) | Workers, orchestrator, draft-verify |
| [performance.md](./performance.md) | Telemetry, caches, turn economy (deep) |

## Surfaces & product

| Doc | Topic |
|------|--------|
| [webgui.md](./webgui.md) | Web / desktop UI |
| [project-structure.md](./project-structure.md) | Source tree + file prefixes |
| [PROJECT_SKILL.md](./PROJECT_SKILL.md) | Build rules for editors/agents |
| [PLAN.md](./PLAN.md) | Near-term checklist |
| [ROADMAP.md](./ROADMAP.md) | Shipped vs backlog (long registry) |

## Niche

| Doc | Topic |
|------|--------|
| [portable-mcp.md](./portable-mcp.md) | MCP packages beside data dir |
| [builtin-skills.md](./builtin-skills.md) | Skill catalog zip |
| [telemetry.md](./telemetry.md) | Low-overhead phase timers (WebGUI) |

If a path in an older note does not match a file name, search the package
under `internal/` — the **concept** is stable; file names use prefixes after
the 2026-07 refactor.
