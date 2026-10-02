# Cold skill-read audit — 2026-10-02

Base: 2b4c132 (dev11). This audit changes no production code. It compares current local Pi, DeepSeek Harness, OpenCode and Codex skill mechanisms with SuperCli, then checks one candidate through the actual scheduler and worker registries. Portable evidence is under .tmp/goal-harness-round9-2026-10-02.

## Existing mechanisms and reachable redundancy

SuperCli already discovers bounded metadata, loads only a selected skill body, caches its guidance, injects it append-only and reads referenced resources without replaying the body. Builtin materialization is serialized and protected by a completion marker. The 1,410 builtin SKILL.md entries total 12,982,556 uncompressed bytes; their metadata-only median is 7,863 bytes. Eagerly retaining every body or introducing filesystem watchers would add standing memory or idle work.

One shared SkillApplier can nevertheless perform duplicate cold body reads: it releases its cache mutex before Discoverer.Get, and a second cache check deduplicates publication after the reads have already happened. The tool is read-only, so an actual coordinator batch can run calls concurrently. Restricted worker registries preserve its bound receiver through RegisterFrom, making the same concurrency reachable across workers.

An ignored fixture runs real Loop.invokeToolCalls with registered apply_skill and actual restricted registry clones. Both a coordinator batch and four worker loops admit four selected-body reads, return four correctly paired caller-local results and publish one shared activation. The callback does not bind a Loop or session, so no wrong-conversation activation defect was found. No provider Complete method is called.

## Measurement and decision

A same-name serialized adapter provides an upper bound on savings, rather than a production-ready single-flight implementation. All four results remain byte-identical (33,059 bytes), and the activation count remains one. Three 300 ms samples, CPU=4, Windows amd64 / Ryzen 7 5800X3D:

| Cold four-caller body fixture | Original batch | Serialized upper bound | Allocated bytes before → after |
| --- | ---: | ---: | ---: |
| 8 KiB | 188.94 µs | 130.84 µs | 246,636 → 92,770 |
| 64 KiB | 871.38 µs | 433.27 µs | 1,876,855 → 600,829 |

The bounded six-session sample contains 2,980 tool calls and zero apply_skill calls. It does not establish that skills are unused everywhere, but it provides no observed incidence for this cold collision. The candidate is deferred: approximately 58–438 µs per collision does not currently justify more load/cancellation/Reset ownership machinery. These allocations are transient, not measured RSS; warm reuse, model input tokens, results and turns are unchanged.

## Neighbor mapping and future boundary

- Pi's core/skills.ts keeps discovery metadata and asks the model to read a selected file. SuperCli already loads selected guidance in one call; adopting a catalog-wide prompt would increase standing tokens.
- DeepSeek Harness uses visibility-matched catalog digests and appends selected skill instructions. SuperCli already has cached metadata and append-only guidance. Its filesystem watchers were not proposed for SuperCli.
- OpenCode retains parsed content through InstanceState. Any future adaptation should coalesce only the selected Get, rather than retain all builtin bodies.
- Codex caches metadata by effective configuration/cwd with explicit invalidation. Adding another metadata cache would not remove this selected-body race; SuperCli already caches its discoverer catalog.

A future port must preserve per-caller validation, activation and delivery; avoid persistent caching of failed reads; preserve retries after file repair; and test Reset against an in-flight load. One caller's cancellation must not cancel another waiter. Sharing whole Execute results or blindly borrowing guidance after file mutation is not supported by this experiment. The deferred lead is retained for a later observed parallel-skill workload, with no current prompt, cache, timer or dependency added.
