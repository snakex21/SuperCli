# SuperCli Project Skill

Use this file as the first project-specific instruction source before editing or building SuperCli.

Mechanism documentation (how the agent loop, delegation, caching, and config defaults work — and why) lives in `docs/`; start with `docs/quickstart.md`.

## Project shape

- Language: Go.
- Main CLI binary: `./cmd/supercli` → `supercli.exe` / `supercli`.
- Web GUI binary: `./cmd/supercli-web` → `supercli-web.exe`.
- Web GUI assets are embedded from `internal/webgui/assets/*` via Go `//go:embed`.
- After changing HTML/CSS/JS in `internal/webgui/assets`, rebuild `supercli-web.exe`; there is no separate frontend build step.

## Required Windows web GUI build

Always build the Windows web GUI with the GUI subsystem flag:

```powershell
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
```

Reason: without `-H windowsgui`, Windows opens an extra console window behind/over the web GUI.

## Verification after web GUI changes

Run at minimum:

```powershell
go test ./internal/webgui
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
```

If provider/request formatting is touched, also run:

```powershell
go test ./internal/llm
```

If projects/workspaces are touched, also run:

```powershell
go test ./internal/storage/memory ./internal/app ./internal/webgui
```

## Web GUI UX conventions

- Keep the rail visible; side panels may collapse.
- Preserve `data-i18n` attributes and the existing `t()` translation flow.
- Keep vanilla JS; no frontend framework or bundler.
- Settings modal should have stable dimensions and scroll internally.
- Topbar dropdowns must not be clipped by parent overflow.

## Projects/workspaces behavior

- CLI project state uses both:
  - `projects.json` for path → project memory key.
  - `workspace.json` for named projects + active project.
- Web GUI must mirror CLI semantics and also hot-swap `Engine.home` for future web requests when selecting a project.
