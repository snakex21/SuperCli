# Compaction cache audit — 2026-10-08

No production compaction change was adopted in this pass. The adjacent harnesses
use different policies: DeepSeek preserves a request prefix, while Pi explicitly
disables retention for a one-shot summary. This is not enough evidence for a
universal replacement in SuperCli.

Metadata from session 55a7401bf03e6792: six summary helpers took 64.50 seconds,
0.476% of the session wall time. Their input was 153,978 uncached tokens; the first
following main requests added 474,132 uncached tokens. The combined 628,110
accounts for 13.61% of session uncached input. Overall cached input was 93.49%.
These observations show an opportunity to measure cache reset cost, not a proven
speedup from replaying full history.

The two small summary requests had 223 and 303 input tokens. Replacing those with
full history could make a helper substantially more expensive. Also, archived
compaction evidence is not the same as the main request projection. Older exact
wire projections are not retained, so an honest historical A/B cannot be
reconstructed from the database.

A future live comparison should use synthetic exact wire projections and measure
the summary plus the next main request together: cost, cached input/cache writes,
TTFT, total wall time, request bytes and summary quality. Preserve native signed
reasoning and provider routing. Do not assume disabling tools preserves cache;
Anthropic documents tool-choice changes as message-cache invalidations. No extra
system instructions, full request snapshots or history copies were added.

References in the adjacent source trees:

- deepseek-harness-master/packages/compaction/compaction-basic/src/summarizer.ts
- pi-main/packages/coding-agent/src/core/compaction/compaction.ts
- [Anthropic prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)

The read-only metadata reproduction script is kept in the ignored outer
workspace .tmp/harness_compaction_metadata.py. It emits counts and lengths, not
private conversation text.
