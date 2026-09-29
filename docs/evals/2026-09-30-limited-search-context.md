# Context for a search capped at a few matches

Automatic search neighborhoods used to be disabled as soon as the result cap was reached. A query locating a declaration with max=1 therefore omitted its body, even when a few adjacent lines contained the answer.

The shared search tool now offers radius-4 context for at most three returned matches, including capped searches. The existing 2 KiB automatic-output budget, long-line handling, explicit context=0 behavior, match cap and incomplete-results notice remain intact. Larger outputs retain the compact locations. There are no new prompt instructions or provider-specific changes.

## Controlled live continuation

Run on 2026-09-30 through the shared provider factory and existing Zen transports. Each case starts after a fixed search of a synthetic retry.go with max=1. The task asks for the integer returned by RetryDelay(). The baseline uses context=0, equivalent to the former capped automatic behavior; the trial leaves context unspecified. Only read/search tools are registered. Separate conversations and isolated portable experiment folders are used. The model does not receive the expected value in the question.

| Free Zen model | Result | Extra file reads | Continuation model calls | Input tokens | Elapsed | Initial tool output |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| muse-spark-1.3-contributor-free, locations | correct | 1 | 2 | 2818 | 6.131 s | 136 B |
| muse-spark-1.3-contributor-free, automatic | correct | 0 | 1 | 1299 | 3.523 s | 228 B |
| space-bunny-free, locations | correct | 1 | 2 | 2316 | 3.634 s | 136 B |
| space-bunny-free, automatic | correct | 0 | 1 | 1120 | 1.489 s | 228 B |

One paired trial per model: these timings include network and generation variation and are not a general speed guarantee. Counts exclude the model turn that originally produced the seeded search. The concrete benefit in both continuations was removing one read and its next model round. Small result caps were uncommon in the inspected history; this is a bounded improvement for that case, not evidence of a broad historical bottleneck.

Reproduce with scripts/search-limit-context (arguments: free model ID, absolute output JSON, absolute public Zen catalog JSON). Output and synthetic files stay under the output directory. The harness deliberately clears API credentials and preserves production Zen routing and gates.

## Validation

- New regression fails before the fix for caps 1, 2 and 3.
- Go fallback and real ripgrep pass; selected-hit counts and incomplete-results notices are preserved.
- Oversized neighborhoods, broad results and explicit context=0 retain compact output.
- Native and sentinel agent routes deliver the body in the next request.
- go test ./... -timeout=90s passed on Windows amd64.
