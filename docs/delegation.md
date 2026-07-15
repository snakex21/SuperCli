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
with a short system prompt, a tool allowlist and its own step budget
(builtin.go).

**Hard limits.**
- **Depth 1, structurally.** `restrictedRegistry` strips
  `task`/`send_message`/`task_stop` from every worker registry, including
  the inherit-everything case — a worker can never spawn a worker, no
  matter what a spec says.
- **Step budget**: spec value, else config `task_max_steps`, else 10.
- **Token budget**: config `task_max_tokens` stops the child loop
  mid-flight; the partial report still returns (failed status).
- **Timeout**: `TimeoutPerStep × MaxSteps` (30 s/step default).

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
