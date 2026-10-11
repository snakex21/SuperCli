# Concurrent preflight and optional compaction backend construction

This follow-up removes client-side work that can be proved unnecessary without
changing model selection, reasoning effort, summary instructions or retained
conversation requirements. It does not claim to fix the historical 116-second
inference-runtime pause. The separately running VM work is left untouched.

## Production change

Overlapping native preflights of the same repository and resolved Git executable
share one in-progress `git log`. Each caller still reads its own current
`git status`, resolves PATH, owns its deadline and gets the same briefing budget.
The completed cache keeps its existing two-second TTL; hits do not renew it.
Completed and in-flight entries now both include the executable in their key,
so one Git installation cannot supply another installation's cached result.
Injected runners retain independent execution. A cancelled follower stops its
own wait; a healthy follower retries a cancelled leader with its own runner and
deadline. Completed flights are removed, with at most 64 pending source keys.

The GUI resolves optional `compact_model` configuration when it creates a loop,
but builds its real metered transport only when that loop actually summarizes.
A loop-local resolver reuses a completed construction, including an ordinary
construction failure that falls back to the main provider. Cancelled construction
does not poison a healthy waiter. No provider proxy is introduced, so existing
native transport interfaces remain available. Configuration, temperature,
workspace and factory selection are pinned to the existing loop.

Construction order is preserved when initialization affects the main model's
metadata or mutable authentication: model-ID collisions, broad OpenCode model
discovery, Codex account/options snapshots and the legacy Zen catalog lookup.
Those cases retain the former eager construction. This is transport/lifetime
compatibility logic, not a model-family or download-site optimization.
TUI/batch keep their existing optional-provider construction order. The shared
preflight optimization and bounded metadata discovery apply across frontends.

Default factory discovery can observe caller cancellation. OpenCode's optional
model-list request now has a ten-second deadline even with the normal generation
HTTP client, which intentionally has no whole-request timeout. A shorter caller
deadline wins. Generation timeouts and user reasoning controls are unchanged.
Legacy custom factory constructors retain their API and can only be checked
before/after construction; this is not a claim that every constructor has become
interruptible.

## Measurement boundaries

Native Git measurements use the same immutable portable fixture on both sides.
Gated cold groups force log reads to overlap, while every caller runs an actual
status process. An independent frozen-behavior legacy loader supplies the
comparison; collected results go through the normal briefing assembler. Every
briefing must match byte for byte. Counts cover subprocesses, not tool calls or
model calls. The gated result does not establish how often cold overlap occurs
in a typical session or prove the latency of a whole agent turn.

GUI measurements use full loop construction, portable on-disk configuration and
the real registry. Separate test binaries replace only the two GUI production
files with their frozen pre-edit versions. Both use the same current factory and
summary code. A deterministic construction/completion boundary adds no artificial
delay, network request or inference. Controls cover inherited configuration and
an actual first `CompactNow`; the latter still needs its selected provider and
one helper call. Removing unused construction must not be presented as speeding
up summary generation.

Five paired samples alternate before/after order, with GOMAXPROCS=1 and serialized
Go compilation. Resources are sampled only before and after the completed
measurement operation. Those endpoint observations cannot describe intervening
VM activity. Stable operation counts are stronger evidence than small timing
differences under concurrent host load. No progress polling is used.

## Completed results

The native overlapping collection/assembly samples favored the candidate in
all five pairs for both group sizes. Each side has only 2–4 native groups per
sample, so timings are observations of this fixture, not a production percentile.

| Fixture | Before median | After median | Native processes per group | Allocated bytes per group, before → after |
| --- | ---: | ---: | ---: | ---: |
| 4 overlapping cold readers | 61.864 ms | 45.928 ms | 8 → 5 | 722925 → 448702 |
| 8 overlapping cold readers | 93.246 ms | 65.683 ms | 16 → 9 | 1398348 → 808826 |

These median reductions are 25.8% and 29.6% for this bounded native collection
fixture. Status counts remain 4 and 8 respectively; log counts become 1.
Four-reader ranges are 55.663–80.965 ms before and 45.084–51.544 ms after;
eight-reader ranges are 82.325–118.502 ms before and 59.935–70.375 ms after.
Allocated bytes are cumulative Go allocation, not peak RAM, GPU memory or a
measured application working-set reduction.

| Full GUI fixture, deterministic backend | Before median | After median | Backend constructions | Model completions |
| --- | ---: | ---: | ---: | ---: |
| Inherited backend control | 7.024 ms | 6.939 ms | 0 → 0 | 0 → 0 |
| Unused compact override | 6.904 ms | 7.165 ms | 1 → 0 | 0 → 0 |
| Loop creation and first actual summary | 7.910 ms | 7.607 ms | 1 → 1 | 1 → 1 |

The unused-override timing is 3.8% higher at the median (only 2/5 faster pairs);
first-summary and inherited-control ranges overlap as well. The cheap
deterministic constructor provides no evidence of a GUI latency improvement.
The proved change is avoiding unnecessary initialization on unused overrides,
with an actual summary still constructing and invoking its selected provider.
Deferring this work does not eliminate the cost of the first summary.

A separate five-sample **unpaired** warm-cache bookkeeping control increases
from 224.5 ns to 256.4 ns median, with zero allocations on both sides. This
31.9 ns lookup overhead includes the stronger source key and cancellation
check; it is not hidden as a gain and is negligible next to a status subprocess.

The measurement's Windows-reported free physical memory changed from
24.731 GiB to 24.378 GiB. Endpoint samples cannot attribute that change to
SuperCli or the concurrent VM work. The optional performance-memory CIM class
was unavailable; the first attempt stopped before executing any benchmark,
and the corrected script uses the supported operating-system memory fields.

## Validation and evidence

Regression tests cover cold/expired/warm Git state, independent roots/executables,
fresh file status, cancellation, healthy retry, ordinary errors, pending-map
bounds and cleanup. Factory tests cover real metering, configuration snapshots,
one concurrent construction, separate loop scopes, failure caching and follower
cancellation. GUI tests compare the complete helper and subsequent main request,
retained recent turns, deterministic file facts, registry descriptors and tool
visibility, capability metadata, accounting and construction lifetime. Cancelled
compaction must preserve conversation history and launch no fallback.

The completed native test suites pass for `internal/system/preflight`,
`internal/llm/factory`, `internal/llm`, `internal/agent`, `internal/webgui`
and `internal/app`; `go vet` passes for the same packages. Serialized testing
and compilation take 316.940 s overall, with 7.866 s for vet. These validation
wall times include compilation and host load and are not agent latency samples.

Both native Windows executables build successfully in 8.566 s and replace the
local CLI/GUI atomically, with SHA256-verified previous binaries retained in
the portable stage. CLI `--version` succeeds. GUI `--help` is also verified by
an explicit native-process blocking wait with redirected pipes: exit 0 and
1618 bytes of usage text on stderr. This additionally avoids relying on the
PowerShell invocation of a GUI-subsystem executable. Installed binary and
backup hashes, measured test-binary hashes and all frozen candidate-source
hashes pass independent verification. No running application is terminated.

The first run exposed two incorrect new fixture expectations: a synthetic file
result omitted the built-in success metadata, and a raw-provider purpose was
confused with the metered default purpose. The fixtures were corrected against
the existing contracts; production fact classification and metering were not
weakened.

Private artifacts stay in `.tmp/efficiency-continuation-2026-10-11/`: frozen
sources, overlays, test logs, validation receipt, paired benchmark logs, resource
receipt and binaries. The historical runtime investigation remains in
[cache allocation and paging](2026-10-10-cache-allocation-paging.md). It identifies
allocation/checkpoint copies with pagefile activity as a backend investigation
direction; it does not isolate the full historical interval or justify disabling
cache, reducing context, changing reasoning, or installing a runtime patch.
