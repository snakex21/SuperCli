# Runtime context limits and model selection — 2026-10-10

The selected model now uses the capacity reported by its actual loaded backend
instance when available. Advertised catalog maxima, including OpenRouter data,
remain useful sources when runtime metadata is unavailable. Runtime capacities
are memory-only and scoped to the endpoint, credential and exact model/instance
ID; they do not overwrite the global capability catalog.

## Cause and resolution

The reported Qwen conversation contained four recent main usage snapshots with
a saved context window of 1,010,000. That value was displayed directly from the
historical request record. Its original configuration/catalog source cannot be
proved from the usage row alone.

The active backend's native API instead reported a loaded capacity of 100,608
and an advertised maximum of 262,144. The supplied settings screenshot showed
100,545 in the context setting; SuperCli uses the loaded-instance API response
rather than assuming the setting and runtime capacity are identical.

Native discovery previously consumed only advertised context maxima. It now
also parses `loaded_context_length` and
`loaded_instances[].config.context_length`, as documented by the
[LM Studio model-list API](https://lmstudio.ai/docs/developer/rest/list).
Instance identifiers stay opaque and case-sensitive. Exact instance IDs use
their own capacity; an ambiguous base model key uses the smallest positive
capacity of its loaded instances. Malformed runtime values cannot replace
valid sibling instances. No model name or model-family capacity is hardcoded.

| Resolution case | Effective window |
|---|---:|
| Loaded instance 100,608; larger configured/catalog window | 100,608 |
| Loaded instance 100,608; explicit smaller budget 32,000 | 32,000 |
| No runtime metadata; known catalog entry | Existing catalog/config resolution |
| No usable metadata/config/catalog | Existing marked local/remote fallback |

The loop, GUI usage recorder, selected-context stats and scoped worker windows
share this policy. A runtime capacity can also exceed an advertised maximum;
the actual instance remains authoritative. Compaction takes its trigger from
the same effective window. User-selected thinking and backend model settings
were not changed.

## Refresh and concurrency

Runtime metadata is refreshed before a user turn and when changing models.
Distinct main/worker endpoints refresh concurrently under one two-second
operation budget. Concurrent readers of the same endpoint and credential share
one request; canceling one waiter does not cancel another, while canceling the
last waiter cancels the underlying work. Individual tools and worker steps do
not cause repeated metadata refreshes. There is no polling or inference here.

An unsupported native API is remembered until explicit model-cache invalidation
or a successful native discovery. Network/auth failures remain optional and
do not prevent ordinary model use. Metadata snapshots and unsupported entries
are bounded; active read handles disappear after completion. Invalidating a
connection cancels/detaches its previous refresh and makes late results obsolete.
Newer accepted results also obsolete older reads, including after cache eviction.
An older unsupported result cannot suppress a newer supported discovery.

TUI model switching rebinds the captured connection used by the resolver and
refresh hook. New default workers follow the selected parent provider; explicit
worker providers and existing worker continuations retain their own backend.
An explicitly injected AgentTool provider retains its existing constructor
contract, including providers implemented as non-comparable values.

## GUI behavior

Model, provider and manual-budget changes refresh stats immediately after the
successful mutation. A late model catalog, stats response or config response
cannot restore the previous selection. Identical catalog refreshes do not issue
extra stats requests. The completed turn carries its own model identity.

The previous main request retains its recorded denominator and is labeled as
the last-turn context. A distinct selected model/window receives one additional
limit line. This avoids assigning a fabricated usage percentage to a model
that has not yet received a request. A new main call records its actual effective
window. Historical positive snapshots, conversation content and cost totals
were not rewritten.

## Verification

- Full suites passed for llm, providers, config, app, TUI and webgui; the full
  agent suite passed in the final separate run after correcting its new fixture.
- Vet passed for all seven packages.
- All 261 browser-side tests passed, covering startup, selection, manual budgets,
  pending/stale responses, context display and existing GUI behavior.
- Both command binaries built and their installed `--help` smoke checks passed.
- A read-only metadata probe returned 100,608 in 6.696 ms, selected that limit
  even with a 1,010,000 test config/catalog, preserved a 32,000 explicit budget,
  and computed a 90,548 compaction threshold. Model calls: zero.
- Frozen source hashes matched immediately before installing the binaries.

Evidence is portable under `.tmp/gui-model-context-fix/`: the six-package pass
results in `after-runtime-fix-tests.log`, `agent-final.log`/`agent-final.json`,
`checks.json`, `check-1.log` through `check-4.log`, `ui-tests-final.log`,
`frozen-sources.json`, `installed.json` and `installed-smoke.json`.
Previous binaries are recoverable under
`backups/a36d00a8-5a80-4590-811d-0374f6ae3dce/` in that staging folder.

This is a context correctness and refresh-efficiency change. The metadata timing
does not measure compaction inference, total agent throughput or model memory.
Reopening CLI/GUI loads the installed version; the user's running app was not
restarted.
