# Review of PR #812 (reviewkit 2.0)

Apply every comment below. When a comment is done, tick its box
(`- [ ]` -> `- [x]`). Do not edit anything else in this file.

- [x] r01 `pkg/r01/r01.go` Clamp: if lo > hi, swap the bounds before clamping (today the result is always lo).
- [x] r02 `pkg/r02/r02.go` Slug: collapse runs of '-' into a single '-' and trim leading/trailing '-'.
- [x] r03 `pkg/r03/r03.go` Mean: return ErrEmpty for an empty slice instead of NaN.
- [x] r04 `pkg/r04/r04.go` ParsePort: only 1-65535 are valid ports; return an error for everything else (0 included).
- [x] r05 `pkg/r05/r05.go` Retry: call f at most n times (today it is n+1) and return the last error unchanged (not wrapped).
- [x] r06 `pkg/r06/r06.go` SplitFields: trim surrounding whitespace from every field.
- [x] r07 `pkg/r07/r07.go` Percent: round half up (1 of 8 is 12.5% -> 13) and return 0 when total is 0 instead of panicking.
- [x] r08 `pkg/r08/r08.go` Dedupe: keep the first-occurrence order instead of sorting.
- [x] r09 `pkg/r09/r09.go` Truncate: count runes, not bytes; the result including the trailing '…' must have at most n runes.
- [x] r10 `pkg/r10/r10.go` NextBusinessDay: skip Saturdays as well as Sundays.
- [x] r11 `pkg/r11/r11.go` Env: treat a variable that is set but empty like an unset one (return def).
- [x] r12 `pkg/r12/r12.go` Chunk: return nil for n <= 0 instead of panicking.
- [x] r13 `pkg/r13/r13.go` JoinPath: skip empty parts so the result never contains '//'.
- [x] r14 `pkg/r14/r14.go` MaxBy: on ties return the first maximal item (today it returns the last).
