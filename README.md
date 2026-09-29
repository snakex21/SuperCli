[English](docs/readme/en.md) · [Български](docs/readme/bg.md) · [Čeština](docs/readme/cs.md) · [Dansk](docs/readme/da.md) · [Deutsch](docs/readme/de.md) · [Ελληνικά](docs/readme/el.md) · [Español](docs/readme/es.md) · [Eesti](docs/readme/et.md) · [Suomi](docs/readme/fi.md) · [Français](docs/readme/fr.md) · [Hrvatski](docs/readme/hr.md) · [Magyar](docs/readme/hu.md) · [Italiano](docs/readme/it.md) · [Lietuvių](docs/readme/lt.md) · [Latviešu](docs/readme/lv.md) · [Norsk bokmål](docs/readme/nb.md) · [Nederlands](docs/readme/nl.md) · [Polski](docs/readme/pl.md) · [Português (Brasil)](docs/readme/pt-BR.md) · [Română](docs/readme/ro.md) · [Русский](docs/readme/ru.md) · [Slovenčina](docs/readme/sk.md) · [Slovenščina](docs/readme/sl.md) · [Srpski (latinica)](docs/readme/sr-Latn.md) · [Svenska](docs/readme/sv.md) · [Türkçe](docs/readme/tr.md) · [Українська](docs/readme/uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
A portable AI coding agent written in Go, with a terminal interface (TUI), a desktop/web interface (GUI), and batch mode sharing one engine.

<!-- readme-unit:status -->
This README describes version `1.0.0`. Portable release bundles are distributed through [GitHub Releases](https://github.com/snakex21/SuperCli/releases); a local build does not imply that its release has already been published.

<!-- readme-unit:h.screenshots -->
## Screenshots

<!-- readme-unit:screenshot.gui -->
![SuperCli web interface (GUI)](docs/screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperCli terminal action centre (TUI)](docs/screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
The GUI image is a screenshot; the TUI image is a rendering of the application's actual terminal layout. [More screenshots](docs/screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Get started

<!-- readme-unit:start -->
Extract the bundle for your operating system and architecture into a writable folder. On Windows, launch `supercli.exe` for the terminal or `supercli-web.exe` for the GUI. Keep the included `supercli-data/` folder beside the executables.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
On Linux or macOS, use the corresponding executable from the bundle or build from source. Select your project with `--home`; use `--batch` for one prompt without the TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Portable data

<!-- readme-unit:data -->
Settings, sessions, memory, credentials, caches, logs, and backups live in `supercli-data/` beside the application. Move the whole application folder to carry them with you. The application does not use `%APPDATA%`, `%LOCALAPPDATA%`, or the Windows registry for its state.

<!-- readme-unit:workspace -->
`--home` and `SUPERCLI_HOME` select the workspace without moving application data. Project overrides and workspace artifacts use `<project>/.supercli/`.

<!-- readme-unit:override -->
Only an explicit `--data-dir` or `SUPERCLI_DATA_DIR` overrides the data location. If the application folder is unwritable, startup reports an error instead of silently using a profile directory.

<!-- readme-unit:legacy -->
The terminal can copy legacy `~/.supercli` data into an empty portable directory on first start, retaining the original. See [data layout](docs/data-layout.md).

<!-- readme-unit:secrets -->
Credentials travel with the portable folder. Keep that folder private, back it up, and never commit API keys or authentication files to a public repository.

<!-- readme-unit:h.config -->
## Models and configuration

<!-- readme-unit:providers -->
Configure providers through the GUI settings or TUI `/providers` and `/models`. Supported connections include OpenAI-compatible endpoints, native Anthropic, ChatGPT/Codex OAuth, opencode gateways, and an offline echo provider.

<!-- readme-unit:config -->
Global settings are in `supercli-data/config.toml`; `<project>/.supercli/config.toml` can override them. Environment variables and CLI flags take precedence. An example local endpoint:

```toml
default_provider = "local"
default_model = "qwen"
language = "en"

[[providers]]
name = "local"
type = "openai"
base_url = "http://localhost:1234/v1"
api_key = "lm-studio"
model = "qwen"
```

<!-- readme-unit:config.note -->
Replace the example endpoint and model with your server's values. Cloud services may require credentials and charge for usage. Model capabilities determine vision, tools, reasoning, and context limits. See [configuration](docs/configuration.md).

<!-- readme-unit:h.features -->
## Features

<!-- readme-unit:surface -->
GUI and TUI support the same 27 interface languages. They provide streaming conversations, session recovery, project selection, model management, attachments, and usage views. The TUI supports mouse scrolling, transcript search, and collapsible thinking/tool output.

<!-- readme-unit:agent -->
The engine supports tool discovery, verified file edits, command execution, session history, project memory, context compaction, goals, worker delegation, consultation, and optional draft models.

<!-- readme-unit:tools -->
Tools cover code search, targeted file reads and patches, images, ZIP archives, DOCX/XLSX/PDF documents, and bounded context execution. Available tools depend on the selected profile and model; use tool discovery for the current catalog.

<!-- readme-unit:extensions -->
Optional MCP packages live in `supercli-data/mcp/` and start when used. The built-in skills archive lives in `supercli-data/skills/builtin-skills.zip`; missing it does not prevent normal startup. Extensions may need their own runtimes or installed host applications.

<!-- readme-unit:optional -->
Darwin candidate generation, councils, and worker delegation are optional workflows. Parallel model calls can increase resource use and cost. The core executable does not require Node, Python, Docker, or CGO.

<!-- readme-unit:h.controls -->
## Commands and controls

<!-- readme-unit:controls -->
Type `/` for the command palette. Open the TUI action centre with `Tab` on empty input or `Ctrl+K`; use arrows to select actions and `Esc` to return.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Command | Action |
| --- | --- |
| `/help`, `/doctor` | Show help and diagnostics. |
| `/model`, `/models`, `/providers` | Choose models and manage providers. |
| `/resume`, `/export` | Resume or export a conversation. |
| `/status`, `/cost`, `/compact` | Inspect usage or compact context. |
| `/goal`, `/plan` | Manage goals and read-only plan mode. |
| `/diff`, `/undo`, `/redo` | Inspect changes or undo/redo an agent turn. |
| `/darwin`, `/council` | Use optional multi-model workflows. |
| `/update check\|download\|install` | Check, download, or explicitly install an update. |
| `/quit`, `/exit` | Exit the application. |

<!-- readme-unit:keys -->
`Enter` sends a prompt; `Ctrl+C` interrupts. `PgUp`/`PgDn` and the mouse wheel scroll; `Ctrl+F` searches; `Shift+T` toggles thinking; `Shift+E` expands tool output. Prompts entered during a run are queued for a safe next step.

<!-- readme-unit:h.updates -->
## Updates

<!-- readme-unit:update.check -->
Check stable GitHub releases on demand from the GUI About screen, TUI `/update check`, or CLI `--check-update`. Checking does not install anything.

<!-- readme-unit:update.download -->
Download stages the matching portable bundle and verifies its SHA256 against the release manifest. GUI download and `/update download` leave the running installation in place.

<!-- readme-unit:update.install -->
Use the GUI install button or `/update install` to install the staged update. CLI `--update` explicitly requests download and installation. Existing `supercli-data/` is preserved, and replaced executables are backed up locally.

<!-- readme-unit:update.restart -->
Restart manually after installation. Close other copies before installing; running executables may be locked on Windows. If a matching bundle or verification metadata is unavailable, use a verified manual release download. Keep your own data backup.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Documentation

<!-- readme-unit:docs.start -->
Begin with the [documentation index](docs/README.md), [quick start](docs/quickstart.md), [data layout](docs/data-layout.md), and [configuration](docs/configuration.md).

<!-- readme-unit:docs.engine -->
Read [architecture](docs/architecture.md), [delegation](docs/delegation.md), [performance](docs/performance.md), [GUI design](docs/webgui.md), and [project structure](docs/project-structure.md) for implementation details.

<!-- readme-unit:docs.extra -->
Feature guides: [portable MCP](docs/portable-mcp.md), [built-in skills](docs/builtin-skills.md), and [telemetry](docs/telemetry.md). Development tracking: [plan](docs/PLAN.md) and [roadmap](docs/ROADMAP.md).

<!-- readme-unit:reference -->
The [previous full README](docs/readme-reference.md) is retained as a historical reference; older feature descriptions may predate version `1.0.0`.

<!-- readme-unit:h.build -->
## Build and verify

<!-- readme-unit:build -->
Use the Go version specified in `go.mod`. On Windows, `build.bat` builds the TUI, `build_ui.bat` builds the GUI, and `run.bat` builds if needed and starts the terminal.

[docs/releasing.md](docs/releasing.md)

```bash
go test ./...
go build -o supercli ./cmd/supercli
go build -o supercli-web ./cmd/supercli-web
node docs/readme/check.cjs
```

```powershell
go test ./...
go build -o supercli.exe ./cmd/supercli
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
node docs/readme/check.cjs
```

<!-- readme-unit:tests -->
Run the Go tests before a release. The documentation checker verifies all 27 translations, mapped content, headings, links, technical literals, and identical code examples.

<!-- readme-unit:requirements -->
Normal use requires a configured model endpoint or account. Git and ripgrep improve repository workflows; external tools are optional unless a workflow needs them. The GUI additionally requires its platform's webview/browser support. Use `--doctor` to diagnose your installation.

<!-- readme-unit:h.license -->
## License

<!-- readme-unit:license -->
SuperCli uses the [MIT License](LICENSE). Bundled dependencies and content have their own notices; see [third-party notices](THIRD_PARTY_NOTICES.md) and the [skills guide](docs/builtin-skills.md).
