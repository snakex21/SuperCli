# Data layout — workspace vs application state

SuperCli separates **where the agent works** from **where SuperCli stores its
own data**. That is intentional and easy to mix up, because both involve a
folder named `.supercli` in some situations.

## Two roots (always)

```text
┌─────────────────────────────────────────────────────────────────┐
│  HOME  = workspace / sandbox for tools                         │
│  Priority: --home  >  SUPERCLI_HOME  >  current working dir    │
│                                                                 │
│  Tools (read_file, shell, search, …) resolve paths under HOME. │
│  Opening another project must NOT steal this portable copy’s    │
│  settings — that is why HOME and data dir are independent.      │
└─────────────────────────────────────────────────────────────────┘
                              ≠
┌─────────────────────────────────────────────────────────────────┐
│  DATA DIR  = SuperCli application state                        │
│  Priority: --data-dir  >  SUPERCLI_DATA_DIR  >                  │
│            <directory of supercli.exe>/supercli-data            │
│                                                                 │
│  config.toml, sessions.db, memory, auth, caches, logs, …       │
└─────────────────────────────────────────────────────────────────┘
```

Code: `internal/storage/home.go` (`ResolveHome`, `ResolveRuntimeDataRoot`,
`PortableDataRoot`).

### Typical Windows portable run

```text
C:\tools\SuperCli\
  supercli.exe
  supercli-web.exe
  build.bat
  supercli-data\              ← DATA DIR (this SuperCli instance)
    config.toml                 global app config
    sessions.db
    supercli.db
    projects.json
    projects\<name>-<hash>\memory.db
    auth.json / auth-*.json     Codex / ChatGPT tokens
    logs\
    …
```

```text
D:\my-project\                ← HOME when you open that project
  src\
  go.mod
  .supercli\                  ← optional PROJECT overlay (see below)
    config.toml                 overrides for THIS workspace only
```

With `run.bat` or `SUPERCLI_HOME=.` from the SuperCli repo folder, **HOME is
the repo itself**, so you may see:

```text
C:\…\SuperCli\                ← HOME = this project
  .supercli\                  ← project overlay for THIS workspace
  supercli-data\              ← still the app data dir (next to the exe)
```

That `.supercli` next to the source tree is **not garbage**. It is the
per-workspace config folder for the project you are working in.

---

## What lives where

### Application data (`supercli-data/` next to the binary)

| Path under data dir | Role |
|---------------------|------|
| `config.toml` | Global SuperCli settings (providers, defaults, knobs) |
| `sessions.db` | Conversation transcripts / FTS |
| `supercli.db` | Credits, goals, shared SQLite services |
| `projects.json` + `projects/*` | Per-project memory maps |
| `auth.json`, `auth-*.json` | ChatGPT/Codex OAuth (secrets) |
| `models.json` (optional) | User capability catalog edits |
| `logs/` | App + crash logs |
| `pricing_cache.json` | Price cache |
| `checkpoints/` | File undo checkpoints |
| `skills/` | Skill packs (builtin zip may be shipped) |

Default: always **portable** — not `%APPDATA%`, not `~/.config`.

### Project overlay (`<HOME>/.supercli/`)

| Path | Role |
|------|------|
| `<HOME>/.supercli/config.toml` | **Project-level override** of global config |

Resolved as:

```text
global  = <data dir>/config.toml
project = <HOME>/.supercli/config.toml   (if present and not the same path)
merge   = global  <  project  <  env  <  CLI flags
```

Code: `internal/system/config` (`FindTomlPaths`, `ResolveConfig`).

Use project config for things that should follow the **repo** (e.g. prefer a
certain model or `task_model` for this codebase), without changing the global
portable profile of this SuperCli install.

---

## Legacy: user-profile `~/.supercli`

Older builds stored global state in `~/.supercli` (or the Windows equivalent).

On first portable start, SuperCli can **migrate** that tree into
`supercli-data/` next to the exe (see `internal/app/startup_portable.go`).
The old directory may remain with a marker; it is **not** the live default
anymore.

If you still have a **repo-root** or **cwd** folder named `.supercli` that
you created by running SuperCli with `HOME=.`, that is the **project overlay**,
not the legacy user-profile path.

---

## Mental model

```text
                    SUPERCLI_HOME / --home / cwd
                              │
                              ▼
                         HOME (workspace)
                     tools read/write here
                              │
              optional .supercli/config.toml
                     (project overrides)
                              │
                              ▼
              merged with  supercli-data/config.toml
                              │
                              ▼
                    agent loop + providers
                              │
                              ▼
              sessions / memory / auth  ──►  supercli-data/
                    (instance state next to the binary)
```

| Question | Answer |
|----------|--------|
| Where does the agent edit code? | **HOME** |
| Where are my chats and API logins? | **DATA DIR** (`supercli-data/`) |
| Where is per-repo SuperCli config? | **HOME/.supercli/config.toml** |
| Why two “.supercli” stories? | One is **legacy user path**; one is **project overlay** under HOME. Live global state is **`supercli-data`**. |

---

## Practical tips

1. **Copy SuperCli to a USB stick** → take `supercli.exe` + `supercli-data/`.
2. **Open project X** → `--home path/to/X` (or set `SUPERCLI_HOME`). Data dir stays with the binary.
3. **Repo-specific knobs** → put them in `X/.supercli/config.toml`.
4. **Do not commit secrets** from `supercli-data/` or project auth files; `.gitignore` already excludes runtime DBs and `supercli-data/*` (except the shipped skills zip).
5. **Gitignore** treats root `.supercli/` as local runtime noise for the SuperCli *source* checkout when HOME is the repo — fine; project overlays in *other* repos are the user’s choice to commit or not (usually only non-secret overrides belong in git).

---

## Related docs

- [quickstart.md](./quickstart.md) — run & map of packages  
- [architecture.md](./architecture.md) — agent loop (runtime behaviour)  
- [configuration.md](./configuration.md) — knobs in TOML  
- [project-structure.md](./project-structure.md) — source tree layout  
