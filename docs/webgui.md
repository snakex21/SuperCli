# SuperCli Web GUI — design notes

Redesigned 2026-07 around one idea: **restraint**. The conversation is
the stage; everything else is quiet and exactly one gesture away. If a
pixel does not carry information, it goes.

## Principles

1. **Conversation first.** One centered column (max 46rem). No cards,
   no avatars, no chrome around messages. The agent's answer is plain
   text on the page background; the user's prompt is a lightly tinted
   block. Everything that is not the answer (thinking, tool calls,
   loop events) renders collapsed and muted.
2. **One accent.** A single warm orange (`--accent`) marks the
   interactive and the active. Red is reserved for real errors.
   Everything else is a grayscale ramp. No gradients, no shadows
   heavier than a hairline.
3. **Hairlines, not boxes.** Separation comes from 1px lines
   (`--line`) and whitespace on a 4/8px grid, not from nested panels.
4. **Telemetry is a whisper.** Each turn ends with one muted mono
   line: `time · cache/eval/gen · cached% · think · tools`. Glanceable,
   never a dashboard.
5. **Designed empty states.** First launch shows an intentional
   welcome (wordmark, one sentence, three suggested prompts), not a
   blank void.
6. **No decorative motion.** The only animation is functional:
   streaming text appearing and a soft pulse on the status dot while
   the agent works.
7. **It just works.** Dark theme by default (a terminal tool's natural
   habitat), light theme one toggle away. Sensible defaults for all
   settings; every knob shows its *source* (default / manual) like the
   TUI `/settings` panel, so "what did I override?" is always visible.

## Regions

```
┌──────────────────────────────────────────────────────────────┐
│ topbar 48px   ◆ SuperCli · workspace      model ▾  ● status  │
├────────────┬─────────────────────────────────────────────────┤
│ sidebar    │                                                 │
│ 264px      │        conversation column (≤ 46rem)            │
│            │                                                 │
│ + new      │   user block                                    │
│ sessions…  │   › thinking (collapsed)                        │
│            │   › tool row (collapsed)                        │
│ projects…  │   assistant text                                │
│            │   2.4s · cache 12k · eval 1.1k · gen 640 · 91%  │
│            │  ───────────────────────────────────────────    │
│            │   composer                              [Send]  │
└────────────┴─────────────────────────────────────────────────┘
        + control panel (overlay): Settings · Appearance · Models ·
          Providers · Accounts · MCP · Memory · Goal · Usage · Files · About
```

- **Sidebar** (collapsible, Ctrl+B): new session, recent sessions,
  projects (click = switch workspace). Nothing else.
- **Topbar**: wordmark + workspace on the left; model picker and the
  connection dot on the right. The model palette holds search, scan,
  hide/show, "set CLI default" and the reasoning-effort select.
- **Control panel** (gear / Ctrl+,): every management surface in one
  overlay with a left nav. Sections map 1:1 onto the JSON API.

## Loop events in the transcript

| wire type              | rendering                                          |
|------------------------|----------------------------------------------------|
| `message`              | markdown-ish text; `<thinking>` → collapsed block  |
| `tool_call`/`tool_result` | one collapsed row: name · arg hint · duration; expands to args + output. `task` rows show `task → agent` with the briefing |
| `notice`, `compact`    | muted event line (`[compact]`, `[prune]`, preflight cost, draft-verify economics) |
| `worker`               | muted event line: worker kind · status · summary   |
| `done`                 | telemetry line under the answer                    |
| `error`                | red-tinted event line                              |

## Palette

Dark (default): bg `#101014`, surface `#17171c`, line `#26262d`,
text `#e8e8ea`, muted `#9a9aa2`, faint `#68686f`, accent `#e8763d`.
Light: bg `#fafafa`, surface `#ffffff`, line `#e6e6e9`, text `#1c1c20`,
muted `#63636b`, faint `#9a9aa0`, same accent.

Type: system UI stack for the interface, `ui-monospace` stack for
code, paths, telemetry. Base 14px; chat 14.5px/1.65.

## Persistence

UI preferences (theme, fonts, language, keybinds, notifications) live
in the server-side blob (`/api/settings` → `webgui-settings.json`)
because the app-mode window gets a fresh port — and thus a fresh
localStorage — on every launch. Backend knobs live in `config.toml`
via `/api/config`, shared with the TUI `/settings` panel.
