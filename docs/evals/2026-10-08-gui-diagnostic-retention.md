# GUI diagnostic registry retention — 2026-10-08

## Evidence and trigger

The GUI Engine previously retained the complete tool Registry for Doctor after
each loop build. With default adaptive delegation or orchestrator ON, the
registered `task` callback is `AgentTool.execute`, whose receiver retains
`ParentLoop`. That loop owns its conversation, content parts and native image
references. The registry also owns its bounded large-output LRU (32 items,
16 MiB). Consequently, finishing a chat and opening another session could keep
the previous model context and results resident: transcript GET/model restoration
does not construct a replacement loop. No task call is needed for this chain.

This is one retained last loop, not proof that all opened sessions accumulate.
Completed worker histories remain intentionally bounded and available for
continuation. This change does not discard those workers or conversation data.

## Change and compatibility

Registry now creates a non-executable diagnostics handle on demand. The handle
owns only registered/visible counters, its lock and publication revision. It has
no Registry, tool callback, compiled schema, output, Loop or model reference.
Visibility/registration changes publish under the registry's existing lock;
unobserved registries have no handle allocation and only a nil check. Unchanged
operations skip publication. Observed changed operations recount the registered
order without allocating names, preserving the exact union of active/always-on
visibility and ignoring unknown always-on names until registered.

Engine retains this handle for the complete base registry, before the parent
registry is restricted in orchestrator ON. Doctor uses the live counts while
work is active and their last values after collection. Wired/nil status and
zero-visible warnings are unchanged. Provider/model metadata still comes from
the existing Provider, ProviderMgr and capability registry. TUI and batch callers
can continue passing an executable Registry to Doctor.

The production builder returns loop plus base registry privately. Tests that
inspect schemas or register executable fixture tools use that result instead of
an Engine debug pointer. Executable tools and worker registries are never cleared,
patched or replaced for cleanup. No prompt, model request or startup migration is
introduced; persistence and user data are unchanged.

## Validation

- Core tests compare counters to existing Len/VisibleNames after all mutation
  paths, including discovery, overlapping sets, unknown always-on names,
  read_output, copied registrations, resets and concurrent readers/writers.
- Doctor compares complete tool-check status/detail for nil, empty, dormant,
  visible and completed registries without executing lazy callbacks.
- The real GUI builder with synthetic large history tests Loop collection via
  weak pointers. A retained real executable Registry is a positive legacy
  control for `task.Fn -> ParentLoop`. No finalizer is attached to a GC cycle.
- GUI tests preserve base-registry parity for adaptive/orchestrator modes and
  execute deterministic Echo work/delegation with the original callbacks.
- `BenchmarkRegistryDiagnosticsVisibility` compares current nil/live handle
  mutation costs and allocations. An ignored overlay experiment compares exact
  pre-edit/current production sources and aggregate retained heap above a warmed
  Engine (18 MiB history plus 2 MiB image field), without models or user data.

Experiment files: `.tmp/optimization-oct8-2026/diagnostic-retention-experiment/`.
Baseline saved before editing. Central Diagnostic regression tests passed in
core, Doctor and WebGUI, including the legacy/current weak-pointer controls.
The editing agent ran no Go test, build or application. Subsequent central
integrated validation passed all 71 Go packages, `go vet ./...` and 208 Node UI
tests. Both applications compiled for Windows amd64, Linux amd64/arm64 and macOS
amd64/arm64. Windows EXEs were installed with matching hashes, old EXEs retained,
and the installed TUI version and working diff check passed. The final source
digest is `4af9f20b78079d019784f9972e1a6a69d3d9ea6ffd4f6157d188f2b66c88068b`.
This remains development version 1.0.4, without a commit, push or release.

## Central measurements

Both overlays compile the actual saved pre-edit/current production sources and
inject the same ignored fixture. Retained-heap runs use a warmed Engine and a
20 MiB synthetic payload: 18 MiB assistant text plus 2 MiB image base64. No model
executes. Three explicit GC cycles run before the baseline heap sample and after
seeding the loop; `runtime.MemStats.HeapAlloc` records the retained-heap delta.
The Engine stays strongly reachable while a weak pointer checks its former
loop's liveness. Each overlay was measured three times (`count=3`).

| Variant | Retained heap deltas, bytes | Median, bytes | Loop retained |
| --- | --- | ---: | --- |
| Legacy executable registry | 20,977,640; 20,971,952; 20,971,968 | 20,971,968 | true in all three runs |
| Current diagnostic handle | 576; 16; 16 | 16 | false in all three runs |

The median difference is 20,971,952 bytes, approximately 20 MiB, for this fixture.
It confirms that the diagnostic handle releases the known
`Registry.task.Fn -> ParentLoop -> history` retention chain. It does not promise
a 20 MiB reduction for every real session: retained context/output sizes vary,
and active or intentionally retained workers may legitimately own references.
Explicit GC measures collectibility after collection, not how quickly normal
GC or the operating system returns memory. This measures Go heap, not process
RSS, native image surfaces or WebView memory; small residual deltas include
allocator/background noise.

The identical visibility benchmark uses 120 registered tools and times one
Activate/Deactivate pair. Handle discovery/reflection and fixture construction
are outside the timed section. Reported central medians:

| Variant | ns per pair | Bytes per pair | Allocations per pair |
| --- | ---: | ---: | ---: |
| Legacy, no handle | 72.63 | 0 | 0 |
| Current, no handle | 77.96 | 0 | 0 |
| Current, live handle | 1,912 | 0 | 0 |

The unobserved path adds 5.33 ns per pair, approximately 7.3%, with no allocation.
Observed mutations cost approximately 1.9 microseconds per pair because each
changed publication recounts the registered-order visibility union. This is a
measured registry-mutation cost, not an end-to-end application CPU or latency
claim. Snapshot reads and unchanged/idempotent mutations are outside this
benchmark, and no prompt or model-call overhead is added.
