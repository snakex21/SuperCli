# Lazy GUI file-change lists — 2026-10-08

## Evidence and behavior

A read-only aggregate of the portable history found 619 recorded turns. Of
these, 35 have more than eight file changes; the largest array has 36,714
entries. No private paths or conversation contents were copied into fixtures.

The existing GUI folded summaries above eight entries and grouped their paths
by change kind, but still constructed every hidden path element immediately.
That work happened both after a live turn and when replaying a session.

Bulk summaries now build their counts, directory roots and three group headers
immediately. Each group materializes all its path nodes only when opened, using
the existing one-shot disclosure helper. A fragment batches insertion; opening
the outer summary alone creates no path nodes. Reopening a group preserves the
same nodes without duplicating work. Its callback releases the retained entries
after rendering. Dropping a transcript releases unopened callbacks with it.
Lists of at most eight changes remain expanded immediately.

Directory roots use string boundaries rather than splitting every path into
segment arrays. The result retains the previous lexical semantics, including
Windows separators, Unicode, empty components and case-sensitive paths; it
does not resolve paths or touch files.

## Measurements

The isolated benchmark and preserved pre-change source are under
`.tmp/optimization-oct8-2026/gui-filechanges`. It uses Node v24.19.0 and optional
LinkeDOM 0.18.12, installed only in that ignored portable evaluation directory.
Neither the application nor the mandatory UI test runner gains a dependency.
Seven warmed samples per variant report medians; input creation and fixture
initialization precede timing/heap snapshots. `--expose-gc` measures the JS heap
still retained after construction, separately from temporary heap before GC.
These are DOM-construction/JS-heap measurements, **not WebView2 process RSS,
layout, paint, frame rate or end-to-end model latency**.

| Synthetic changes | Before ms | After ms | Before retained JS B | After retained JS B | Before elements | After elements |
|---:|---:|---:|---:|---:|---:|---:|
| 32 | 0.617 | 0.499 | 75,416 | 35,208 | 55 | 23 |
| 256 | 2.475 | 1.160 | 333,080 | 48,416 | 279 | 23 |
| 4,000 | 29.320 | 14.029 | 4,561,848 | 215,288 | 4,023 | 23 |
| 10,000 | 50.887 | 22.642 | 11,337,664 | 466,792 | 10,023 | 23 |
| 36,714 | 212.271 | 71.915 | 41,480,896 | 1,576,344 | 36,737 | 23 |

Temporary heap before GC in the largest case fell from 76,368,992 to 17,506,456
bytes. The remaining construction work includes normalization, deduplication,
grouping and directory calculation. Full paths remain retained until opened;
this is not a claim of constant total memory. Opening all groups still creates
all path nodes and eventually pays their construction cost. The benefit is
avoiding this cost for folded groups, not deleting information.

The exact seven-sample results are in `metrics.json`. Run the preserved
benchmark using the local Node runtime:

```text
node --expose-gc .tmp/optimization-oct8-2026/gui-filechanges/bench.cjs
```

## Validation

The LinkeDOM fixture compared complete expanded HTML with the preserved
pre-change implementation for 1, 8, 9, 32, 256 and 4,000 changes. Small-list
HTML, escaping, order, headers and relative paths remained identical; repeated
toggle preserved nodes. It also compared directory roots across 10,000
deterministic randomized sets.

The dependency-free `test/ui/file-change-summary.test.cjs` verifies normalization
and counts, a 12,000-entry list, independent group disclosure, exact paths,
stable node reuse, immutable input and directory parity including edge cases.
Initial fixture failures were setup errors (an unmatched outer disclosure state
in the comparison and loading shared globals after the transcript); both were
corrected before adoption. No production error was inferred from those failures.

The integrated Node runner is validated centrally alongside the other changes
in this pass. No provider schema, prompt, agent turn or instruction is added
by this GUI change.
