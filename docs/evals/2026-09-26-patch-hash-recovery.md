# Stale patch hash recovery — 2026-09-26

## Evidence

The fixed GunMayhem snapshot (through message 1571) contains 38 commands mentioning Get-FileHash. Most are not proven redundant: some verify packaged binaries or inspect files after other edits.

Three patch_file rejections due to an outdated base_hash were followed by a separate hash command for the same source file:

| Rejected patch result | Following hash command | Recorded command time |
| ---: | ---: | ---: |
| 605 | 608 | 239 ms |
| 730 | 731 | 228 ms |
| 1281 | 1282 | 234 ms |

The patch implementation had already read and hashed the exact file snapshot to reject the edit. Its error discarded the digest, forcing the caller to obtain it again.

## Change and limits

The mismatch error now includes current_hash from that existing digest and asks the caller to review current contents before retrying. It still rejects the edit and writes nothing. A retry checks the supplied hash again; the diagnostic cannot bypass an intervening user/worker/formatter edit.

No additional reads/hashing were added to successful patches or targeted reads. The read_lines API, optional hash guard, tool schema and permanent instructions are unchanged. Reviewing changed source may still require read_lines; this only removes the need for a separate hash-only command. Mixed-purpose hash commands can still be useful.

The observed command times total 701 ms, but this is not a measured end-to-end saving: the historical commands were sometimes batched with other work. No provider latency or model-choice claim is made.

## Validation

- Before: a real tool-registry test failed because the mismatch result lacked the current digest.
- After: the caller obtains the digest from model-visible error output, reviews current source and successfully retries without a hash tool.
- An intervening external edit still rejects the earlier digest and leaves all file bytes unchanged.
- LF/CRLF and Unicode fixtures pass; existing patch matching/atomicity tests pass.
- Full project tests and go vet pass. Artifacts: .tmp/patch-hash-2026-09-26/ and the shared final validation under .tmp/check-identity-2026-09-26/.
