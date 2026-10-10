# Preflight and compaction — 2026-10-10

This change reduces preparation work while preserving the user's model and
thinking selection. The coding, program-use and download paths continue to use
the same agent loop. No website-specific policies or new inference calls were
introduced.

## Changes

- Preflight shows the HEAD commit once. Only the exact duplicate at the start of
  the recent-commit list is removed; every other entry keeps its order.
- Git porcelain status preserves both leading columns. Previously, trimming all
  whitespace corrupted the first modified path in a large status summary.
- Grouping changed paths uses string slices instead of splitting and joining
  each pathname. Output, including quoted paths and rename destinations, stays
  byte-for-byte compatible.
- CLI, batch and GUI worker preflight use the invocation context. Cancelling
  collection stops before backend probing, schema/loop preparation, worker
  registration or a model request. The legacy embedding callback remains valid.
- Compaction assembles each message once and reserves the transcript buffer.
  Its input text is identical, including tool-result excerpts and UTF-8 edges.
- A single instruction string can be reused without copying; multipart text
  reserves its final buffer once. Canonical history remains untouched.
- Summary framing is 150 rather than 285 bytes. The 135-byte saving repeats in
  subsequent requests containing the summary. The actual summary, exact facts,
  protected recent turns and reasoning/native provider data are preserved.
  Both old and new complete summary envelopes remain recognizable on resume.
- A shorter compaction instruction explicitly preserves all still-applicable
  user requirements and prohibitions, including earlier ones, as instructions.
  Past compliance alone does not replace a prohibition. The Goal/Done/State/
  Pending template and distinction between plans and observed results remain.
  The instruction decreased from 973 to 870 UTF-8 bytes.

The existing 300-token preflight budget, fresh uncached working-tree status,
two-second static Git cache, compaction limits and configured
compaction provider are unchanged. There is no new persistent cache.

## Local preparation benchmarks

Windows amd64, Ryzen 7 5800X3D, Go 1.26.2. Three 300 ms samples per case; medians
below. Original implementations are preserved as independent test oracles or
loaded through a frozen source overlay. Model generation did not run concurrently
with these benchmarks. Timing can still vary with other machine activity.

| Preparation fixture | Before | After | Temporary allocations before → after |
|---|---:|---:|---:|
| Compaction, 40 ordinary coding turns | 0.829 ms | 0.162 ms | 749,946 → 189,826 B; 173 → 2 allocations |
| Compaction, 40 turns with fragmented text | 11.205 ms | 0.584 ms | 22,887,980 → 1,156,481 B; 2736 → 42 |
| Compaction, 40 turns with tool calls/results | 0.726 ms | 0.208 ms | 1,460,383 → 519,168 B; 496 → 122 |
| Preflight, 500 changed paths, canned Git | 0.110 ms | 0.069 ms | 67,928 → 35,928 B; 1057 → 57 |
| Extract one 24 KB instruction string | 0.015 ms | below 0.001 ms | 24,576 → 0 B; 1 → 0 |

The fragmented transcript uses approximately 95% fewer allocated bytes. This
measures temporary Go allocations during preparation, not total process RAM,
model RAM or inference time. Preflight measurements exclude Git subprocess
latency; the unchanged collector uses three Git calls on a cold fixture.

The eight-commit clean fixture decreased from 202 to 184 estimated prompt tokens;
six changed paths decreased from 263 to 245. The 500-path fixture remains at 294
because the status section already fills the budget. Another exact-fact example
decreased from 98 to 86. These are SuperCli estimates, not provider token counts.

Evidence in the ignored portable staging folder
`.tmp/coding-efficiency-fix/`: `benchmark-receipt.json`, `focused-1.log`,
`focused-2.log`, `preflight-final.log`, frozen `before-sources/` and the baseline
overlay. No application data was redirected to a user-profile directory.

## Real model continuation

The real model was `qwen3.8-27b-uncensored`, with thinking enabled and effort
`max` in both variants. The compaction fixture is a completed real coding turn
plus a long user-supplied maintenance brief and two neutral recent user turns.
The older prefix includes actual failed tests, the actual pricing patch and
actual passing tests. The next ordinary main request asks for the preserved
state and restrictions, with no operations. Both variants compacted 13 messages;
the retained raw tail could not supply the tested coding facts.

Fixture SHA-256:
`e42dfd29a02143df4551e8ea2b6eb728394abceb3dd0170cd273b0bb7b75180f`.
The real GUI loop ran `CompactNow` followed by `Run`; no tool results were
fabricated. Both variants made exactly two model calls and executed zero tools.
Main tool definitions stayed identical at 12,030 encoded bytes.

| Whole compaction continuation trial | Before | Final change |
|---|---:|---:|
| Compactor time | 36.993 s | 38.845 s |
| Next main request time | 20.699 s | 21.200 s |
| Combined time | 57.694 s | 60.046 s |
| Compactor input tokens | 8215 | 8180 |
| Next main input tokens | 5050 | 5084 |
| Combined input tokens | 13,265 | 13,264 |
| Wrapped summary bytes | 1959 | 1916 |
| Required facts/constraints in summary | 8/9 | 9/9 |
| Required facts/constraints in continuation | 8/9 | 9/9 |

The baseline lost the earlier portable-data requirement in both the summary and
the next reply. A wrapper-only intermediate trial also lost the future no-publish
instruction, representing past compliance instead. The final generic prompt
preserved both constraints as future instructions, as well as the pricing path,
zero/default quantity semantics, observed failed-to-passed tests, pending
documentation, protected tests/catalog and no new dependencies. Full-text review
confirmed the facts independently of lexical checks.

This single sequential pair was **4.1% slower overall**. It provides evidence of
better constraint retention and lighter local preparation, not an inference
speedup. The final continuation contained more of the required constraints;
total model input was essentially unchanged. The endpoint did not report usable
cache attribution, so cached-input and cache-hit savings are not inferred from
zero-filled usage fields. First provider-output timing includes reasoning:
compactor 3.337 → 8.468 s; next main request 1.486 → 1.367 s.

Authoritative receipts under `.tmp/coding-efficiency-fix/compaction-runs/`:
`run-4215736429/receipt-revalidated.json` and
`run-2464377315/receipt-revalidated.json`. Original receipts/logs are retained.
The same offline validator corrected Polish wording false negatives without
re-running the model. A preliminary fixture with only one neutral tail kept the
coding turn raw; its diagnostic run is excluded from this comparison.

The separate coding task ran the normal GUI pipeline on a six-file Node project
with an existing failing invoice test. Both variants reproduced two failures,
changed only the pricing implementation and then passed all seven unchanged
tests. An independent final Node run verified the result, and every protected
fixture remained byte-identical.

| Real coding task | Before | Final change |
|---|---:|---:|
| Wall time | 55.463 s | 54.306 s |
| Model calls | 6 | 6 |
| Tool calls | 5 | 6 |
| Input tokens | 40,063 | 40,673 |
| Actual injected preflight bytes | 146 | 98 |
| Estimated preflight tokens | 58 | 44 |
| Operations after successful completion | 0 | 0 |
| Repeated successful calls | 0 | 0 |

The extra candidate search ran alongside the initial directory listing; it was
neither a repeated successful call nor post-completion work. This single sample
was 2.1% faster with the same number of model calls, but it does not establish a
general action, input-token or inference-time reduction. Native definitions were
identical across all 12 coding requests. Receipts:
`runs/run-1872436733/receipt-revalidated.json` and
`runs/run-273533721/receipt-revalidated.json`; combined comparison:
`.tmp/coding-efficiency-fix/eval-comparison.json`.

## Validation

The agent and preflight package tests passed. Independent original-text oracles
passed 2000 randomized cases each for transcript assembly, instruction extraction
and opaque path grouping. Regression tests cover real Git stdout, cancellation,
old/new summary envelopes, archive/projection persistence, repeat-compaction
guards and summary text not becoming a human download authorization.

The first frontend run passed CLI/app and TUI. GUI found two assertions tied to
the old summary wording; they now require recognition of the complete summary
envelope. The TUI test explicitly checks that neither envelope leaks into chat.
Some checkpoint fixtures also exceeded Windows' path limit when Git appended
`.lock` to a 255-character path. A shorter portable test directory,
`.tmp/ce/temp`, resolves this without modifying checkpoint production code.
The first failure log remains in `frontend-first-attempt.log`.

Final checks passed: complete agent/preflight package tests, affected compaction
tests after the prompt change, CLI/app and TUI suites, updated TUI summary test,
the complete GUI suite, and `go vet` for all five affected packages. Both
`cmd/supercli` and `cmd/supercli-web` built successfully. The installed binaries
passed `--help`, SHA-256 verification and the GUI embedded-assets check.

Both application-folder executables were replaced atomically. The previous
versions are kept under
`.tmp/coding-efficiency-fix/backups/1dfb37a8-7870-48c3-91ff-ae001fa4cbf0/`.

Installed SHA-256:

- CLI: `62973EFDAE63D43CC6AE7C2B4A907DAE271F97B3B77C6E733607E4CC52217735`
- GUI: `31D611EEBF2BB5A1BA830F063CFC4C508A1B33A913E0C7914606C381D3484700`

Portable receipts: `focused-checks.json`, `checks-first-attempt.json`,
`checks.json`, `installed.json`, `help-smoke.json` and `source-proof.json` under
`.tmp/coding-efficiency-fix/`. Restart the running application to load the new
executable. No repository commit or push was made.
