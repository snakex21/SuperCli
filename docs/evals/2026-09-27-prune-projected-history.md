# Tool-result pruning uses projected history — 2026-09-27

## Confirmed defects

The request estimate already applied native-reasoning policy and omission of
retrievable completed tool evidence. Pruning nevertheless selected victims and
calculated its minimum gain from the canonical visible history.

Deterministic tests through maybePruneToolResults reproduced:

- An older completed turn: claimed reclaim of 27045 tokens, actual request
  estimate unchanged at 4010. The operation still rewrote history and retained
  another archive.
- The latest completed turn: the same claimed reclaim, but request estimate
  grew from 3966 to 4285. Pruning made formerly oversized completed results
  small enough to fit the recent-evidence budget and reenter the request.
- Discarded completed native reasoning inflated the minimum-gain denominator,
  preventing useful pruning of results that actually remained in the request.

Evidence: .tmp/prune-projected-history-2026-09-27/red.json.

A related retention interaction matters even after excluding already omitted
victims: shrinking one retained recent result can free room for another omitted
result. Subtracting individual result sizes alone therefore overstates savings.

## Change

Pruning reuses the existing visibility/reasoning/resolved-tool projection and
source-index mapping. It selects and prices only tool results present in that
view. The protected newest results and current assistant tool batch are located
in the same view; accepted mutations still target their canonical indices.

Before retention or prefix mutation, a candidate batch is projected again.
Only its net reduction can pass the existing minimum-gain threshold. A failed
archive save uses the same check with the shorter no-handle markers. Reports
use that projected net reduction.

The additional candidate check runs only after the trigger, refusal memo and
cheap gain gate allow a proposed batch. There are no additional model calls,
instructions, schemas or file reads. Ordinary below-threshold preparation and
the existing refusal memo remain unchanged.

## Verification

- All initial red cases pass. The two omitted-output fixtures now make no
  mutation, no archive write and no pruning event; estimated requests stay
  4010 and 3966 respectively.
- Discarded reasoning no longer blocks genuinely useful pruning; reported
  savings match the change in the request estimate.
- The recent-budget fixture rejects a batch whose net savings are too small
  after other results reenter the view.
- A combined hidden-history/omitted-history/active-tools fixture maps four
  actual victims to the correct canonical messages. It writes one bounded
  archive, preserves the current call/result pair, and read_output retrieves
  each original without another source read. Other messages stay unchanged.
- Existing pruning, archive fallback, protocol, compaction and refusal-memo
  tests pass.
- Full go test -timeout=90s ./..., go vet ./..., CLI and GUI builds pass.
- CLI --help passes; installed binary hashes match the new builds. Backups,
  test outputs and source comparisons remain inside the application directory.

Installed:
- supercli.exe: 0b97fc2056780f36f2476c8a0f4e13b98e331ad68fe42083353374ba846c478c
- supercli-web.exe: c4f854b814b956894eee52ac950db17d0e75096e0b3d2c86d8a8be4012cb1eb5

These regressions exercise the full coordinator history path used by ordinary
coding and workers. Numbers are deterministic local estimates, not measured
provider token usage or a live-session speedup. Provider transports, including
OpenCode Zen, and portable storage paths are unchanged.
