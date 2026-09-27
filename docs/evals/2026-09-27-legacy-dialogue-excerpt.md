# Keep useful dialogue and recent outcomes in session recall — 2026-09-27

## Confirmed defects

The GUI's legacy-session fallback read the last 16 stored records. Tool traffic could displace useful user/assistant dialogue and leave too little material to produce a summary. Separately, the shared recall renderer shortened each summary from the beginning only, discarding the latest outcome at its end.

An initial Python screening undercounted dialogue because it did not decode all persisted JSON key variants. Its zero-dialogue GunMayhem estimate and aggregate candidate count are not confirmed findings. Exact replay through the application's Go decoder corrected that measurement; no message decoder change was needed.

## Change

Legacy recall reuses the existing ReadDialogueExcerpt reader, selecting up to eight useful recent dialogue rows plus the original user task when needed. The callback is the same webCapsuleText filter used for saved session capsules. Tool logs, reasoning-only rows and malformed parts are not turned into dialogue.

The shared renderer keeps both ends of a long summary with an explicit omission marker, within 720 UTF-8 bytes per preview. This also applies to already-saved capsules. Middle details can be omitted; the full stored conversation is unchanged. Existing conversation-count limits and addon budget policy remain unchanged. Token estimates are approximate and the existing closing footer is outside the per-entry budget counter.

No new model call, permanent instruction, provider route or storage location was introduced. OpenCode Zen is unchanged.

## Saved-data replay

Selected original records were copied in one read-only SQLite transaction and imported into isolated portable test stores. The fixture preserves original content and parts/tool-call JSON. Logs contain counts, not private message text.

| Saved session | Useful raw-tail rows → excerpt rows | Retained content/parts bytes before → after |
| --- | ---: | ---: |
| GunMayhem de105c910d16e47e | 7 → 9 | 139,160 → 147,423 |
| USOS 97f11945104d6cb4 | 1 → 5 | 36,527 → 6,875 |

In both fixtures, the latest capsule ending was missing from the final recall before the change and present after it. USOS previously had only one useful row in the raw tail, insufficient for a capsule.

The byte counts describe selected stored payloads, not prompt tokens or all bytes scanned by the reader. GunMayhem retains more source text after the change; no universal reduction in read volume is claimed.

## Local measurements

Three warmed runs of 20 operations, Windows/AMD Ryzen 7 5800X3D:

| Benchmark | Before | After |
| --- | --- | --- |
| Legacy recall with a large tool tail | 0.843–0.980 ms/op | 0.738–0.780 ms/op |
| Allocated bytes in that fixture | 351,001–354,992 B/op | 70,056–72,179 B/op |
| Allocation count in that fixture | 382–383/op | 697/op |

The synthetic tool-tail case allocates about 80% fewer bytes but more individual objects. The existing ordinary 100-session benchmark remains comparable: 1.146–1.423 ms/op in the previous batch versus 1.181–1.273 ms/op now.

These are local retrieval measurements. No reduction in real-model turns, provider latency or whole-session duration has been measured for this change.

## Verification

- Red/green tool-tail regression preserves the original task and latest result while excluding logs and reasoning-only records.
- ASCII and multilingual previews preserve both endpoints, an explicit omission marker, valid UTF-8 and the 720-byte cap.
- Two opt-in saved-excerpt replays pass.
- Existing full-history capsule parity, project selection and workspace-alias checks pass.
- Full go test -timeout=90s ./..., go vet ./..., and both executable builds pass.
- CLI --help and git diff --check pass.
- Portable CLI/GUI copies installed with backups and SHA-256 verification.

Artifacts are in .tmp/legacy-dialogue-excerpt-2026-09-27/: private saved excerpts, red/green results, benchmark results, full checks and installation records. The preliminary tail-audit.json is superseded by the Go replay described above.
