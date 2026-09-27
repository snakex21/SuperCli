# Retain captured locations when search context reaches its cap

Date: 2026-09-27

## Evidence

The fixed GunMayhem snapshot contains 117 search_code calls and 33 search results carrying the 500-line context-cap notice. That count demonstrates use of this path, not 33 proven redundant subsequent searches. No live session was polled.

Current renderSearchContext collects all locations up to the requested search limit, then expands neighborhoods. Once the 500-line display budget is exhausted it previously discarded the remaining captured locations on the ordinary path. A saved-output handle could expose only the already-capped context, leaving the model to search again for omitted locations. An earlier long-line path sometimes retained locations; ordinary capped results did not.

## Change

At the existing cap return, retain the already-collected location list followed by the existing bounded context preview. Locations come first so default retrieval does not start by repeating the same context. Keep both sections: adding locations must not discard the context previously recoverable through read_output.

The visible context still has the same 500-line budget. Its cap notice now refers to attached locations instead of instructing a new context=0 search. Regular uncapped output is unchanged. Search limits and their existing incomplete-result notice remain in the saved location list. The shared output store still governs capacity, persistence, retrieval and eviction.

No extra file scan, model request, fixed prompt instruction or provider-specific path is introduced. Captured results remain historical evidence, not a claim about files after later edits.

## Tests

- A synthetic 20-match source has separated neighborhoods exceeding the cap. Native and thin agent replays perform one search and one read_output query. The source is deleted after search completes. Before the fix, the requested last location is missing; after the fix, matches.txt:1180:needle_19=6842 reaches the next provider request.
- Both the actual bundled ripgrep backend and the Go fallback pass that replay. Three scripted provider requests cover search, retrieval and final report; this is not a live-model turn-saving measurement.
- Separate tests cover 20/24 captured locations, a reached search-result limit, multiple files, long original matching lines, UTF-8, retention of the context preview and no extra stored payload for ordinary small searches.
- Full go test -timeout=90s ./..., go vet ./..., CLI and GUI builds, CLI --help and git diff --check pass.

## Cost and limits

The retained object gains the captured location list and two short section labels. In the short-line fixture it grows from an approximately 8 KB context to 8.9 KB stored output; with a long original matching line, retained data is about 16.9 KB. A capped result that previously fit inline now also carries the existing roughly 180-byte retrieval footer (8,110 visible bytes -> 8,290 bytes including the footer in this fixture). These costs occur on the capped path; no general prompt budget is increased.

The change makes previously collected evidence retrievable without a repository rescan. It does not show that a live model will always choose retrieval, nor prove an end-to-end latency reduction.

Artifacts: .tmp/search-context-retention-2026-09-27/ includes the saved-audit counts/sequences, red/green protocol and backend replays, retention cases, full suite/vet/build logs and installed binary hashes.
