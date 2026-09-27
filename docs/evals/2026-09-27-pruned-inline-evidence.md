# Recover inline evidence after context pruning

Date: 2026-09-27

## Confirmed gap

The previous pruning fix preserved existing output-store handles. Ordinary inline results did not have one, so pruning replaced them with a details-omitted marker. Keeping the tool arguments did not make the previous observation retrievable: rereading changed or removed files could not recover its historical contents. Worker loops need not have a transcript writer either.

The fixed GunMayhem snapshot contains 370 tool results of 700–8192 UTF-8 bytes without a visible large/stored-output envelope (212 read_lines, 72 ctx_execute, 45 search_code, and other tools). This is scale screening, not evidence that those exact records were pruned or caused repeated work.

Four current-code regressions failed before the change. They perform 40 real file reads, remove each source file after reading, then prune the results. Both native and thin invocation paths lost retrieval, with and without persistence.

## Change

An accepted pruning pass packs previously inline results into one archive in the existing output store. Each marker points to that result's byte offset and bounded read_output limit. The archive is capped at 4 MiB, prioritizes the newest pruning candidates, and occupies one cache entry rather than evicting itself by adding more than the existing 32-entry limit.

Planning accounts for the longer reference markers before the minimum-gain decision. A result too short to justify its reference stays inline. The combined text is built and saved only after pruning is accepted; rejected scans and ordinary inline tool calls do not save extra outputs. Existing references are reused without re-saving their contents. Oversized batches keep the existing details-omitted fallback for results outside the archive budget.

Only selected tool result text is archived. User/assistant messages, hidden history, protected recent results and applied skills remain outside it. Existing status parsing, protocol pairs, pruning thresholds, output-cache limits and transcript persistence are preserved. The special Zen transport and system instructions are unchanged.

## Verification

- Native/thin × memory/persisted replay retrieves all 40 original results after their source files are removed. Forty original source reads remain forty; retrieval uses the saved archive.
- Persistent variants write the full original transcript, persist the pruned model projection, reload that projection from SQLite, and use a fresh output cache to retrieve the original bodies. The full GUI/TUI transcript remains intact. One archive write and one cold archive read serve all 40 results.
- A second pruning pass does not rewrite the prefix or save the same archive again.
- Trigger/protection/disabled/minimum-gain guards perform no extra saves. One boundary fixture would prune with the former short markers but correctly declines after including reference costs.
- A 700-result fixture enforces the 4 MiB bound, excludes protected/private message categories, preserves exit_code=7, and retrieves from memory even when persistence fails. Short results remain inline, and existing stored outputs are not duplicated.
- go test -timeout=90s ./..., go vet ./..., CLI/GUI builds pass. CLI --help and diff checks are recorded alongside installation hashes.

## Cost and limits

This is an evidence-recovery change, not a measured live-model speedup. It adds reference bytes to affected prune markers and one local archive save per accepted pruning batch. No fixed provider call or additional system instruction is introduced.

For 64 inline results of 3400 bytes, local benchmark measurements were:

| Path | Before | After | Allocated bytes per pass |
| --- | --- | --- | --- |
| Memory only | 0.0466–0.0474 ms | 0.1266–0.1276 ms | 18.5 KB → 297 KB |
| Portable SQLite retention | 0.0467–0.0473 ms | 10.96–11.33 ms | 18.5 KB → 743–746 KB |

The former implementation did no archive I/O and discarded the retrieval path. The added disk cost happens at pruning, not at every read or message. Both before/after benchmarks exclude existing transcript-projection writes to isolate this change.

In the 40-read fixture, native visible-context estimates fall from 23896 to 4095 tokens; thin estimates fall from 24296 to 4495. These are estimator values, not provider token measurements, and do not imply a larger reduction than the former lossy pruning.

Normal bounded retention/expiry still applies. New archives share the existing 16 MiB memory and 64 MiB persisted output budgets and can evict older outputs. A failed database write provides an in-memory reference only; it cannot survive a process restart. Results already lost by an older build are not reconstructed.

Artifacts: .tmp/pruned-inline-evidence-2026-09-27/ contains the reproduction, projection and guard tests, before/after benchmarks, full checks, builds and installation hashes.
