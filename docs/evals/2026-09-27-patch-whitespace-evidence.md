# Exact source evidence for whitespace patch failures

## Fresh evidence

In the code-worker hint experiment, Qwen read cache/store.go but supplied a tab where the file contained one space. The patch tool correctly rejected the edit and diagnosed a unique whitespace-normalized match. It only described the difference, however; the following model request reread the source before the corrected edit.

The pre-edit file was reconstructed from the synthetic fixture and checked against the exact before_hash retained in the successful correction: 60d2a8eab3f1aee0d7b80d2b56c201b8c98162ce4456cdcc1236d1512b7a7abf. Original failed arguments, source and diagnostic are frozen in the replay artifact.

## Change

For a unique normalized whitespace match, the failure now includes bounded, quoted original source from the file buffer already used by patch_file. Small matched spans preserve real indentation and newlines; longer spans show the line at the first difference with the existing bounded snippet helper. Both collapsed-whitespace and removed-whitespace diagnoses are covered.

The normalized byte range is mapped back by scanning the existing content, without an O(file-size) offset array or another file read. The existing four-MiB diagnostic guard and 120-byte snippet bound remain. Successful edits do not run this diagnostic. Ambiguous normalized matches retain their counts and do not select a candidate. This never applies fuzzy edits: exact matching, base hashes and all-or-nothing batch semantics remain in place.

## Verification

- Red/green tests cover the actual tab/space shape, multiline indentation, minified text, CRLF, Unicode, long lines and ambiguous matches.
- Byte-range property cases check the mapping for both normalization forms.
- An optional exact captured replay (SUPERCLI_PATCH_WS_REPLAY) rejects the original call, obtains its source anchor from the error itself and successfully applies the intended correction without an intervening source read.
- Native and thin invocation tests verify failed status plus the same exact whitespace evidence in the model follow-up and UI event; the rejected patch leaves the file unchanged.
- Full go test ./..., go vet ./..., CLI/GUI builds and CLI help smoke pass.

Artifacts and executable backups: .tmp/patch-whitespace-evidence-2026-09-27/.

No new model instruction, background request or OpenCode Zen route change was introduced. The replay proves that repair evidence is available immediately; it does not prove that every live model will choose to skip the next read, and no live wall-clock speedup is claimed.
