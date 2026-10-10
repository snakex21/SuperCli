# Universal agent action economy

## Behavior

The universal core prompt now directs every tool call toward an unmet requested outcome or a missing piece of evidence. Existing paths, URLs, handles and results should lead directly to the relevant action. Tool discovery is needed for missing contracts, and checks should be repeated for failures, relevant state changes or missing evidence. Batch tools are preferred over separate calls for independent work. The prompt preserves reading missing context before edits and completing all requested outcomes; the core remains within 1,200 characters (1,197).

The first experiment appended a bounded historical tool-batch index and a next-action reminder. It preserved correctness but failed to reduce unnecessary searches in the live sample, so it was removed. The trace showed the model repeatedly planning the correct operation while selecting the easier native search function. Resource readers and requested effects only had full contracts behind the generic `invoke_tool` dispatcher.

The replacement changes contract exposure rather than adding another reasoning layer. Only registered simple read-only follow-up targets declared by core tools receive additional native definitions. Selection is deterministic and capped at eight tools, 8 KiB of name/description/schema bytes and eight scalar fields each. These tools were already permitted through the simple read-only dispatcher. Selection does not activate or discover tools, and it uses only the current restricted registry. The read set is frozen per registry and reset on registry replacement. Unrelated read contracts stay in the existing compact catalog.

Initially requested contracts can also be exposed natively, with a combined cap of sixteen additional tools and 16 KiB of descriptor bytes; existing core definitions are separate. They are already admitted by the existing request/workflow logic; unrelated dormant effects are excluded. This snapshot is reset at Run entry and exit, frozen on the first coordinator request, and stable through subsequent discovery and tool execution. Later admitted contracts retain their ordinary dispatcher path. A change of requested capabilities can alter the native tool prefix once at a new instruction boundary, trading that prefill cost for avoiding repeated incorrect model decisions. Exact native definitions are excluded from the request-only dispatcher tail to avoid contradictory call instructions.

Additional outputs, pending asynchronous work, requested inspections, fresh observations and independent research remain legitimate. No searches are redirected, no website/model branches or new inference requests are introduced, and permissions, effect replay and read-cache semantics are unchanged. Full native schemas still use ordinary validation and tool authorization.

## Live comparison protocol

The baseline GUI test binary was compiled before production edits, and each candidate binary contains a frozen production snapshot. All use the same local Qwen model, maximum thinking, ordinary GUI reconstruction, native tools and automatic delegation. Runs are sequential, without competing model requests or concurrent heavy root checks. Every application-data and output directory is isolated within the portable repository's `.tmp` folder.

The general-operation fixture uses byte-identical prompts and three local source files. The first turn asks for an owner, port and two calculated durations. The second asks for two distinct output formats and explicitly requires reading both back. The evaluator checks actual values, JSON fields, completed replies and successful read-back after both creations. No scripted provider, fake tool result or evaluation-only prompt guidance is supplied.

The existing download conversation remains a regression scenario: one output, a follow-up into another folder, then two different outputs together. Source discovery and HTTP remain real. Asset choices, network responses, reasoning and sampling can vary, so this single comparison does not establish a general timing guarantee.

Baseline receipts:

- General operations: `.tmp/agent-operation-live/run-2783919036/receipt.json`.
- Downloads: `.tmp/gif-followup-live/run-1140323248/receipt.json`.

The general baseline passed in 44.76 seconds: five model calls and four tool calls. It already batched the three source reads and the two output inspections efficiently. The download baseline passed in 148.86 seconds: fourteen model calls, thirteen tools, no failures and four valid animated files. It repeated the same search twice in its second turn (served by read-result reuse) and used another redundant prior search in its third turn.

## Validation

Final deterministic regressions cover generic registered workflow names, exclusion of unrelated/complex/mutating tools, count/byte/property caps, deterministic ordering, exact immutable descriptors, restricted registries, unchanged activation/discovery, native-vs-dispatcher capability availability, stable schemas during a Run, fresh Run and registry lifetime, route escalation and tool-free final replies. Existing multi-output download, ordinary argument/verification checks, checked operation replay and export authorization tests remain in place. The context report now counts actual native schemas; request-token estimation is checked against the real request view. An independent code review found no required safety correction. The complete final agent suite passed in 21.581 seconds; the core prompt suite also passed.

The removed footer experiment passed correctness but used 16 model calls and 21 tools over 170.769 seconds for downloads, compared with baseline 14/13 over 148.442 seconds. It performed eleven URL-only searches, nine on already known URLs, and six exact repeat searches in a turn. The unchanged general fixture retained five model calls and four tools but took 46.832 seconds. These intermediate receipts remain available at `.tmp/agent-operation-live/run-1296055682/receipt.json` and `.tmp/gif-followup-live/run-1944692291/receipt.json`; `.tmp/agent-operation-live/comparison.json` records that failed experiment. This variant is not installed.

The broad native-read experiment eliminated repeated and URL-only searches in its download sample: 11 model calls, 9 tools and 95.062 seconds, with four valid GIFs and zero failures. Exact schema hashes were stable across every request. However, its general fixture used seven tools instead of four (separate reads replacing batch tools) and took 61.999 seconds. This led to removal of unrelated native-read fallback choices and the explicit batch-tool preference. Broad receipts are `.tmp/agent-operation-live/run-1578420561/receipt.json` and `.tmp/gif-followup-live/run-771254180/receipt.json`; `.tmp/agent-operation-live/comparison-with-native.json` records them. This broad variant is not installed.

## Final narrow-contract results

Times below sum measured turn durations and exclude test-harness setup. They consistently use the same metric for both variants.

| Scenario | Baseline | Final narrow variant |
| --- | --- | --- |
| Facts, two reports and required read-back | 44.734 s; 5 model calls; 4 tool calls | 42.133 s; 5 model calls; 7 tool calls |
| Three download turns, four animated GIFs | 148.442 s; 14 model calls; 13 tool calls | 115.572 s; 12 model calls; 11 tool calls |

The final download sample had zero exact repeated calls/searches, URL-only searches, explicit refreshes or tools after the last save in every turn. All four files passed GIF decoding and size/hash inspection; the last two were distinct. A guessed page failed, and the model recovered by finding a valid source. Input tokens fell from 117,372 to 96,902; reasoning tokens from 1,945 to 1,591. Observed wall time was 22.1% lower. Different selected assets and ordinary sampling/network variability make this a single observed comparison, not a universal speed guarantee.

The general fixture passed all factual, output and required read-back checks. It retained five model rounds, but Qwen selected separate parallel reads rather than `read_many`, increasing tool invocations from four to seven. These were distinct necessary reads, not repeated operations. The batch preference therefore remains guidance rather than a guaranteed model behavior; this sample does not show an across-the-board tool-count reduction.

The exact ordered native schema hashes were stable for every request within each turn: fifteen definitions in the general scenario and sixteen for downloads. No helper or auxiliary inference calls were introduced. Final receipts are `.tmp/agent-operation-live/run-3399429508/receipt.json` and `.tmp/gif-followup-live/run-4066768413/receipt.json`. `.tmp/agent-operation-live/comparison-final.json` preserves all eight baseline/intermediate/final scenarios, including rejected experiments.

Final integration tests passed for `internal/tools/web`, `internal/app`, `internal/ui/tui` and `internal/webgui`; `go vet` passed for those packages plus `internal/agent` and `internal/llm/prompt`. Both CLI and GUI builds succeeded. Check logs and timings are in `.tmp/action-economy-fix/checks.json`. Installation uses hash-verified replacement with rollback copies under `.tmp/action-economy-fix/backups`; `.tmp/action-economy-fix/installed.json` records the installed binaries, and `help-smoke.json` records their successful `--help` checks. `source-proof.json` hashes the final sources and report.
