# Changelog

## Unreleased

- r01: Clamp if lo > hi, swap the bounds before clamping
- r02: Slug collapse runs of '-' into a single '-' and trim leading/trailing '-'.
- r03: Mean return ErrEmpty for an empty slice instead of NaN.
- r04: ParsePort only 1-65535 are valid ports
- r05: Retry call f at most n times
- r06: SplitFields trim surrounding whitespace from every field.
- r07: Percent round half up
- r08: Dedupe keep the first-occurrence order instead of sorting.
- r09: Truncate count runes, not bytes
- r10: NextBusinessDay skip Saturdays as well as Sundays.
- r11: Env treat a variable that is set but empty like an unset one
- r12: Chunk return nil for n <= 0 instead of panicking.
- r13: JoinPath skip empty parts so the result never contains '//'.
- r14: MaxBy on ties return the first maximal item

## 1.9.0

- initial import
