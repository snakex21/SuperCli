# Continuous paging of retained tool results

## Confirmed failures

The search-context audit found that multiple hit windows already share one file read. No additional optimization was added there.

A separate current-code regression reproduced two output retrieval defects:

- HeadTail moves its head cut backward to a complete line or UTF-8 boundary. The generated read_output hint still used the fixed byte offset 3072. Following that hint skipped 572 bytes in the LF/CRLF fixture, including the beginning of a diagnostic line, and dropped complete 2-, 3- or 4-byte characters at the boundary in the Unicode fixtures.
- A positive limit smaller than the first UTF-8 character returned an empty successful page with the same next offset. Following the tool's own continuation could repeat forever.

These are deterministic tool-level reproductions, not evidence that either failure occurred in a particular recorded model session. No live-provider latency reduction is claimed.

## Change

HeadTail and result compaction share the same rendering function, which now also returns the actual retained head boundary. The continuation hint resumes exactly there, without a second scan. Public HeadTail output and the GUI/TUI's complete original results remain unchanged.

If a requested page cannot fit its first UTF-8 character, read_output returns that one complete character and advances. This can exceed a requested 1–3-byte limit by up to three bytes; the 8192-byte maximum remains unchanged. Normal pages and explicit offsets retain their existing semantics, including forward alignment of offsets inside a character.

The tool description/schema, fixed model instructions, providers and special OpenCode Zen path are unchanged. There are no additional model calls, disk writes, cache entries or whole-file reads. Loading a cold retained result still happens once; later pages use the existing cache. Previously persisted hints are not rewritten.

Correctly recovering previously omitted bytes can require more output than the old incomplete pagination. This is a correctness/reliability fix that removes a possible repeated-read cause, not a claim that every read becomes faster.

## Validation

- New regressions fail before the fix and pass after it.
- Preview plus pages reconstruct every original byte for ASCII, LF, CRLF and 2/3/4-byte Unicode boundaries, both in memory and through a fresh store backed by persistence. The cold fixtures assert exactly one save and one load.
- Tiny limits finish without empty-page loops. Offset alignment, default/maximum limits, EOF and invalid offsets are covered.
- All core tests, full go test -timeout=90s ./..., go vet ./... and both executable builds pass.

Local evidence: .tmp/output-paging-boundaries-2026-09-27/ (before files, red/green results, suite, vet, builds and installation record). Fixtures contain synthetic text only.
