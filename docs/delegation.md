# Delegation — task workers, orchestrator, draft-verify

The common thread: **the main conversation's context is the scarcest
resource**. Heavy work happens in isolated child loops; only a final
report re-enters the chat. Files: `internal/agent/agent_tool.go`,
`builtin.go`, `orchestrator.go`, `draftverify.go`.

## The `task` tool

**What.** The model delegates a self-contained subtask to a fresh worker
loop with its own context and a restricted tool registry; only the
worker's final report returns.

**Schema.** `{"prompt": "...", "expect"?: "...", "agent"?: "...",
"advise"?: bool}` — a bare `{"prompt": ...}` is valid and gets the
`general` worker (full tool set minus delegation). `expect` is folded
into the briefing ("your final report must contain: ..."). Other builtin
kinds: `advisor` (read-only), `explore`, `plan`, `review`, `code` — each
with a short system prompt and a tool allowlist (builtin.go).

**Hard limits.**
- **Depth 1, structurally.** `restrictedRegistry` strips
  `task`/`send_message`/`task_stop` from every worker registry, including
  the inherit-everything case — a worker can never spawn a worker, no
  matter what a spec says.
- **Step safety net**: custom-spec value, else config `task_max_steps`, else
  the shared 300-step runaway ceiling. Builtin workers do not carry small
  6/8/12-step work budgets; repeat/cycle detection stops real loops.
- **Token budget**: config `task_max_tokens` stops the child loop
  mid-flight; the partial report still returns (failed status).
- **Timeout**: `TimeoutPerStep × MaxSteps` (30 s/step default), additionally
  bounded by cancellation of the parent task.

**Inheritance.** A worker inherits the coordinator's thin-tools flag,
stable-toolset flag and sandbox root, so it is exactly as cache-friendly
and small-model-reliable as the main session. Its cold prefill is the
accepted cost of isolation. If `preflight_repo` is on, the repo-state
block rides the worker's briefing (see performance.md) — a cold context
benefits from it most.

**Why.** The coordinator's history stays lean: the tail cost of carrying
`task` is ~28 tokens/turn, while the worker's whole exploration happens
off-chat. Live-confirmed on qwen3.5-9b (worker does real multi-tool work,
report returns, commit 0009f8f).

## Delegation policy (`orchestrator`, default AUTO)

The setting has three distinct states:

- unset / `auto`: the normal coordinator keeps its full tools and delegates
  only when useful;
- `true` / `on`: hard orchestration restricts the parent to delegation and
  read-only lookup tools;
- `false` / `off`: worker tools are physically absent, so delegation cannot
  occur.

**What.** The HARD delegation switch. The main loop's registry is
physically restricted to delegation + read-only lookup
(`orchestratorTools`: task, send_message, task_stop, tool_search,
read_lines, read_context, list_dir, recall, ask_user, goal, remember).
Every mutating/executing tool is absent — `Registry.Execute` on one
returns "unknown tool". Workers keep the full base registry.

**Why hard, not prompted.** Coordinator mode is a prompt-only nudge that
leaves every tool reachable via tool_search; a model under pressure will
"just quickly edit it itself". The registry makes the boundary a fact,
not a request. The payoff is context economics: heavy tool traffic
happens in workers, and the orchestrator's schema-core is *lighter* than
the normal thin-core (it drops ctx_execute and edit_line — the heaviest
schemas). Measured in the 2026-07-04 live test: main-conversation context
per turn dropped ~41%, and the model delegated spontaneously.

**Why new-session only.** Swapping the tool list mid-session would change
the serialized `tools` block at the front of the prompt and break the KV
prefix. `/orchestrator auto|on|off` persists to config.toml and takes effect
on the next launch.

**When to enable.** Use ON for long sessions where the chat is the control
plane and the work has many delegable chunks. AUTO is the general-purpose
default. Use OFF only when no background/child worker may be created.

## Model-per-task (`task_model`, default empty)

**What.** Workers run on a different model/host than the coordinator.
Two forms: `"model-id"` (same transport, different model) or
`"providerName/model-id"` (a named `[[providers]]` entry — different
host/key).

**How.** `AgentTool.WorkerProvider` + a lazy one-time `WorkerPing`
(GET /v1/models, 5 s) on the *first* delegation, never at startup. An
unreachable worker backend downgrades all delegation to the
coordinator's provider with a single warning line — never a hard error.
Host-specific gates (cache_prompt etc.) are decided per instance from
the *worker's* base URL, so a local worker behind a cloud coordinator
(or vice versa) gets the right hints. Telemetry appends `model=...` to
the worker summary only when it differs from the coordinator, keeping
the single-model output byte-identical.

**Live (2026-07-06):** coordinator Qwen3.5-9B on :8089, worker
Ministral-3-3B on :8091 — delegated worker made exactly one completion
on :8091, report returned; with the knob unset, zero requests hit :8091.

## Draft-verify ladder (`draft_verify`, default OFF)

**What.** For delegated work that changes files: the (small, cheap)
worker DRAFTS → an objective sieve runs for free → the (big) coordinator
model issues a verdict **on the diff and the sieve evidence, never on the
worker's narration**.

```text
task ──► worker drafts (task_model)          [small model, cheap]
              │
              ▼
        sieve: verify_commands in sandbox     [0 LLM tokens]
        (first non-zero exit = RED evidence)
              │
              ▼
        verdict: big model sees prompt+expect
        + git diff + sieve output             [1 short turn]
              │
     ┌────────┼──────────────┐
   ACCEPT   REVISE          TAKEOVER / broken verdict
   return   instruction+RED   hand back draft+diff+evidence
   draft    back to worker,   to the coordinator to finish
            ≤ max_rounds      itself (safe fallback)
```

**Why verdict-on-diff, not on-report.** The live test that shaped this
(2026-07-06, scenario 2): a worker was given a repo with a planted bug
and a failing test; the worker *correctly narrated* the situation but
changed nothing — a confident report over a red tree. The sieve
(`go test ./...`, exit 1) caught it at zero token cost, and the verdict
model said TAKEOVER despite the worker's confident text, because it stood
on the failing test, not the narration. Small models routinely report
success at failure; evidence-bound verdicts are the fix.

**Bounded rounds.** `draft_verify_max_rounds` (default 2) caps REVISE
ping-pong; past the limit the best draft is handed back annotated. A
verdict that fails to parse falls back to TAKEOVER — a broken verdict can
never auto-accept a draft.

**When it pays off** (and why it is OFF by default): (a) the drafter is
much cheaper than the verdict model (small `task_model`), (b) an
objective sieve exists (build/test commands) so junk is rejected before
the big model spends anything, (c) the task is mechanically verifiable.
No sieve + drafter ≈ verdict model = the asymmetry disappears. The
`draft-verify:` telemetry line (outcome · rounds · draft/verify token
split · red sieves) exists to measure this case-by-case.

## Second opinion (`advise: true`)

**What.** `task` with `advise:true` routes to the read-only `advisor`
worker: search+read tools only, one concise recommendation, zero side
effects, never enters the draft-verify ladder (nothing to verify — no
diff). Runs on `task_model` when set, so a "which of these two designs?"
question can go to another model for one cheap turn.

**Why not an N-model council.** A council multiplies cost N× per
question and mostly produces agreement noise; one deliberately-requested
dissenting opinion on a *specific decision* is the useful unit. (A
separate `/council` command exists for the times you really want the
roster.) Live: advisor answered a design question in 1 step / ~500
tokens, sentinel file untouched (read-only asserted by test).

## Reliable continuation and live worker view

A retained worker's `send_message` invocation uses the current parent tool-call
ID and question channel. Its progress belongs to the new continuation card;
earlier reports stay intact, with a link back to the previous delegation.
Parallel workers of the same agent type are matched by call ID.

The GUI shows a compact, collapsible worker overview above the composer:
stable Worker 1/2/... labels, current tool/activity, and running/done/error states.
Click a worker to open its latest card. The TUI keeps a bounded worker panel
above the input; very small terminals use the transcript and `/workers`.
Both views use existing events, with no additional model calls or polling.

Independent `send_message` calls share the task parallelism policy; two calls
to the same worker remain ordered. Local/cloud defaults are based on the
resolved worker backend. Explicit `task_parallel` settings still apply.
Worker tool search/invocation uses the child's own restricted registry, with
stable tool ordering. Thin workers always receive discovery and dispatch tools,
even when a specialized role's functional allowlist omits them. These gateways
can reach only that worker's allowed tools. Only the last assistant report
returns to the coordinator, without intermediate commentary or reasoning.

For a separate `task_model`, CLI/TUI and GUI resolve the tool protocol, stable
schema setting and catalog placement from the worker's actual model/backend.
A failed CLI worker probe falls back to both the coordinator's provider and its
tool/context settings. Existing `small_full_tools`, `stable_toolset` and catalog
overrides still apply; a resumed worker keeps its established profile.

General workers with tool discovery inherit common and already-visible schemas,
while optional tools remain registered and are exposed on demand. Registries
without discovery retain their complete visible set. This avoids sending every
optional tool schema to a native-tool worker just because it was delegated.

Continuing an in-memory worker retains its earlier tool calls and results in
the model context. Inheriting search_history does not make the worker's own
transcript retrievable: that tool may search only the parent/global store.
Completed tool envelopes are omitted only with a configured session writer,
history search, and no pending/lost transcript writes. Context-budget pruning
and compaction still bound long conversations; no extra prompt or model call
is added. Regression tests reproduce the lost evidence in both thin and native
tool modes and verify that the existing conversation prefix survives resume.

Concurrent user questions are queued in TUI and presented separately in the
GUI. Answer, cancellation, and expiry close only the matching question.
Question delivery itself is bounded by its timeout.

Regression coverage: `go test ./...` includes worker continuation across two
web runs, isolated tool discovery, sequential/parallel continuations, concurrent
questions, Unicode layouts, and scroll preservation. Run
`node scripts/test-delegation-ui.cjs` with Playwright available to check real
browser cards, worker overview, continuation links, and narrow layout; set
`PLAYWRIGHT_BROWSER_PATH` for an existing Chrome/Chromium installation.
Browser profile and screenshots stay under `.tmp/delegation-ui`.

These changes do not add model instructions, change OpenCode Zen's special
transport/tool gate, or move application data out of its portable directory.


Within a mixed tool-call response, adjacent independent delegations retain the
same backend parallelism policy as an all-delegation response. A following read
waits for those workers to finish. Two messages to the same worker split groups,
so its continuation cannot race its earlier instruction; completed results stay
in the original call order. This adds no automatic worker creation.
