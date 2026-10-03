# Exact registered tool membership before hardening

Baseline after clean dev17 9a930aa. The narrow production port follows completed semantic proof, one bounded ABBA and compiler escape proof. No live models, provider requests, user applications, settings, builds or commits.

## Narrow source lead

At internal/agent/loop_tools.go:363, every invoke previously called registry.Names before HardenToolCall. Registry.Names creates a slice containing all registered names under its read lock. HardenToolCall first checks exact name membership; when it matches, no later code depends on other registered names. Argument trimming, empty object default, json.Valid, repair and error/retry advice remain in the same function.

Registry.Get and Names read the same tools map, including inactive/dormant registrations. Get does not activate a tool. The non-test registry source files registry.go and registry_catalog.go contain no Registry.Has helper. Registry registration does not replace existing contracts. Activation/visibility are separate and are not inferred from exact membership.

The port changes only this inline hunk, after the existing Zen placeholder rewrite:

    known := []string{tc.Name}
    if _, ok := l.registry.Get(tc.Name); !ok {
        known = l.registry.Names()
    }
    if errMsg := HardenToolCall(&tc, known, l.recentBadCallStreak()); errMsg != "" {
        // existing failure handling, unchanged
    }

A known exact name has the same accepted branch with a one-element input. A missing name still gets the original full Names call, including existing suggestion ordering, retired editor advice, goal action advice, unavailable-tool advice and error/retry behavior. No sorting or policy change is introduced. Custom registered names equal to retired/editor/action names must remain accepted. Misses add an extra Get/read-lock and can be slightly more expensive; this cost must be measured.

Execute still resolves the registered tool/schema again and applies the original coercion, validation and RepairArgs before Fn. Known membership does not authorize an inactive invoke_tool target: resolveInvokeToolCall remains unchanged, including its active/visible/complex/mutation gates. Cancellation, per-call observation, writer/persistence and verification code are unchanged.

## Integration guard

loop_tools.go is also being changed by the runtime agent for dev17. No old full-file baseline may overwrite that change.

prepare-overlay.mjs read the current dev17 source, required exactly one expected hardening site, and changed only the inline hunk. Both overlays use their frozen source files, so parallel runtime provenance edits cannot make the two arms use different loop implementations. Both arms include R14 worker-mutation recovery. The production port rebased only this hunk on the freshest source rather than copying the full prototype.

## Completed semantic proof

Original and candidate focused tests PASS. Final parity script completed in 8.743 s; package times 0.103/0.092 s are test execution timings, not a latency benchmark. Test fixtures were corrected before proof for the actual Delta.ToolCall contract and declared mutation.value schema; these were fixture issues in both arms, not production regressions.

Exact hardening parity covers known/dormant names, valid/whitespace/empty/repaired/unrepairable arguments, scalar/null/array JSON, typo/case/blank names, registered formerly retired/action names, missing/empty registry, retired editor and goal advice, and retry budget. Actual Loop.invoke tests cover dynamic registration after a miss, dormant registered callbacks, schema rejection before Fn, cancellation TOOL_NOT_STARTED and inactive/active invoke_tool guards.

Seven public NewLoop -> Run cases have byte-identical SHA-256 bundles of outgoing Provider message/tool-definition arrays, in-memory history and semantic UI events: exact, repair, bash/read placeholder mapping, typo miss, retired editor and goal action. Fake callbacks execute no command/file read, and the fake Provider has no network transport. This demonstrates Loop-level Zen placeholder adaptation; it is not a live Zen API wire test. Existing provider route is unchanged by the hunk. The initial pair is recorded in terminal-parity.json; the definitive fresh-source controlled-clock pair is recorded in final-public-parity.json, as described below.

## One completed counterbalanced measurement

Windows amd64, Ryzen 7 5800X3D, portable Go caches/TMP, GOMAXPROCS=2; actual Loop.invoke with a fixed small synthetic callback result. Exactly 1/8/64/256/1024 registered tools and three modes per count. Each arm 100 ms/case, original/candidate/candidate/original, no favorable rerun. ABBA completed in 19.936 s. Raw logs bench-1-baseline.txt, bench-2-candidate.txt, bench-3-candidate.txt, bench-4-baseline.txt; structured measurements.json.

| Dispatch case / registered count | Original ns/op | Candidate ns/op | Original B/op | Candidate B/op | Original allocs/op | Candidate allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| known/count_1 | 1697–1776 | 1586–1734 | 1688 | 1672 | 21 | 20 |
| known-repair/count_1 | 1883–1895 | 1803–1826 | 1728 | 1712 | 23 | 22 |
| miss/count_1 | 1014–1027 | 1032–1058 | 912 | 912 | 18 | 18 |
| known/count_8 | 1725–1774 | 1633–1711 | 1800 | 1672 | 21 | 20 |
| known-repair/count_8 | 1950–1969 | 1853–1917 | 1840 | 1712 | 23 | 22 |
| miss/count_8 | 2752–2825 | 2774–2867 | 2592 | 2592 | 32 | 32 |
| known/count_64 | 2531–2625 | 1681–1765 | 2824 | 1672 | 21 | 20 |
| known-repair/count_64 | 2776–3095 | 1837–1863 | 2864 | 1712 | 23 | 22 |
| miss/count_64 | 16618–16641 | 16291–16759 | 16161–16162 | 16161–16162 | 144 | 144 |
| known/count_256 | 4964–5077 | 1659–1699 | 6536 | 1672 | 21 | 20 |
| known-repair/count_256 | 5109–5894 | 1946–2078 | 6576 | 1712 | 23 | 22 |
| miss/count_256 | 64769–65093 | 62026–62907 | 62887–62889 | 62888–62889 | 528 | 528 |
| known/count_1024 | 16344–17270 | 1794–1883 | 20106 | 1672 | 21 | 20 |
| known-repair/count_1024 | 15771–16228 | 2042–2218 | 20146 | 1712 | 23 | 22 |
| miss/count_1024 | 271611–272870 | 274127–275244 | 248526–248534 | 248529–248532 | 2064 | 2064 |

Known valid and repaired calls save exactly one allocation, independently of the count. B/op savings follow the eliminated full Names backing allocation: 16 B at one tool, 128 B at eight, 1152 B at 64, 4864 B at 256, approximately 18434 B at 1024 (allocator rounding and small runtime pool noise included in measured B/op). At 64/256/1024 tools both candidate timing samples are substantially below both original samples; small-registry control remains near the original cost and known/count1 ranges overlap. This is a conditional host dispatch reduction only.

Miss allocation count is unchanged in all five cases. The additional Get/read lock has a real tradeoff: count 1 original 1014–1027 ns versus candidate 1032–1058 ns; count 8 ranges overlap with candidate slightly higher; count 1024 original 271611–272870 versus candidate 274127–275244 ns (roughly one percent higher). Count64 overlaps; count256 happened to be lower in both candidate runs, but the miss algorithm is unchanged and no improved miss timing is claimed. These are short benchmark samples, not application latency or retained RAM.

## Compiler proof

Escape compile completed in 5.616 s, PASS. The actual candidate invoke site reports `internal/agent/loop_tools.go:363:19: []string{...} does not escape`. The error/event allocations remain heap-backed as before; no claim is made that tool dispatch itself is allocation-free. Full compiler log escape-candidate.txt.

## Scope and recommendation

The four-line inline change is justified for exact registered calls at moderate/larger registries: fewer transient allocations and less name iteration with unchanged semantic proof. Missing calls preserve full registry diagnostics at the cost of one extra lookup, explicitly measured above. No new Registry API/cache, instruction, schema, prompt, timer, service or dependency is proposed. Root approved only the exact inline hunk, meaningful execution/public Loop regression fixtures and this report. The port reads the freshest production loop_tools.go and changes only this hunk, preserving both frozen backend provenance assignments and R14 worker-mutation recovery. Production tests omit the mirrored Names/HardenToolCall helper and all benchmark code; isolated full differential and benchmark evidence remains ignored.

Recent historical incidence/registry-size distribution is not measured, and model input/output/tokens, prefill, TPS, required model turns and application RSS are unchanged or unmeasured. This must not be marketed as faster Qwen prompt processing or fewer model turns. The source is reached at each tool invoke, including a resolved invoke_tool recursive dispatch.

Frozen common baseline SHA-256 34d5966761080bef06458f3229c1d2d5b31ac67b57c6807d9fdd01a93f478634; candidate SHA-256 4c21bebcf30a421ca4130ae4b0b7a255a07d3b57c80b063a19c9c20a36aaf546. The port was rebased on fresh production source with SHA-256 d0f9675a6867306f9597ea347bc7def43e5cef9e062cd939b01edb6b95908590. Only the inline Names hunk differs from that snapshot. Final production gofmt, focused KnownToolDispatch tests, agent vet and git diff --check all PASS; the check script completed in 5.352 s. Raw-clock focused tests PASS independently. Full outgoing request SHA changes across minutes because the existing request assembly adds timeSection(time.Now()); this is unrelated to the Names hunk. To compare fresh before/after bytes exactly, both ignored overlays used the same fixed 2026-10-03 12:34 UTC clock in the same loop_complete.go clone. This final pair PASS in 8.645 s and all seven complete request/history/semantic-event fingerprints match. No production clock or hook was changed, and no request field was removed or normalized. The production combined loop_tools.go SHA-256 is bfb300792e71048a60595a7e238d5a58b829936bd9a7c30026b522c055e90e2d. Root performs the remaining full integration. No further benchmark or candidate work remains in this proof.
