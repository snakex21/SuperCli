# Confirmed file facts during compaction — 2026-09-27

## Problem and reproduction

The deterministic appendix below the model-written compaction summary used tool
calls alone, read only the "path" argument, and classified modifications by name
substrings. Consequently:

- Real read_lines/read_context calls use "file" and were omitted.
- patch_file and create_file were classified as reads.
- The first observation of a path prevented a later edit from upgrading it.
- Failed or unexecuted operations could be presented as completed work.
- invoke_tool envelopes and read_many were ignored.

Before changing production code, two real-tool direct/envelope fixtures and an
unpaired/duplicate-call fixture failed. Evidence: portable
.tmp/compaction-file-facts-2026-09-27/red.json.

## Change

CompactFacts now pairs results with the immediately preceding assistant batch,
rejects duplicate IDs and mismatched tool names, and decodes supported dispatcher
envelopes using the existing decoder. No dispatch, permission or tool execution
behavior changes.

Known reads and confirmed mutations use their actual argument schema and tool
result. Failed/blocked operations do not become completed facts. A dry run, noop,
or pruned result can retain a neutral file reference without claiming a change.
Successful modifications take precedence over reads; repeated observations update
recency, with normalized paths used for deduplication.

read_many uses its sequential generated headers and terminal counts to retain
successful siblings only. The code checks the whole batch before accepting those
paths. Ambiguous/truncated output, abbreviated long paths and pruned mixed batches
do not invent per-file outcomes. Source text remains numbered and cannot become
an unnumbered generated section header.

The appendix has a combined 2048-byte limit, with at most 20 entries per category.
Active tools and recent modifications have priority. Whole path entries are kept;
unusual comma/newline-containing names are quoted. No file is reopened.

## Verification

- Real ReadLines, ReadMany, PatchFile and CreateFile executions through the
  registry, with the actual model-facing output renderer, pass in direct and
  invoke_tool forms.
- A large partially failed batch passes through the bounded output-store preview:
  successful siblings survive, the missing file is not reported as read, and
  pruning does not manufacture an individual outcome from aggregate counts.
- Failure, blocked write, noop, Word preview/noop, pruned output, discovery roots,
  unrecognized extensions, mismatched names, flat/string/duplicate envelopes,
  duplicate IDs, user boundaries, recency, path deduplication and byte limits
  have regression coverage.
- The normal auto-summarizer still makes exactly one provider request.
- go test -timeout=90s ./..., go vet ./..., CLI build and GUI build passed.

## Cost and limits

This is a correctness improvement to the shared local/cloud compaction path.
It introduces no permanent instructions, extra model calls or filesystem reads,
and runs only when constructing a compaction summary. The appendix can now include
useful facts previously omitted, so it is not necessarily shorter for every
conversation; its total size is bounded. No end-to-end latency, saved-turn count
or reduction in agent repetition is claimed without a live comparison.

The model-written summary can still be imperfect. This change fixes its
deterministic file appendix, not every possible claim in the generated summary.
