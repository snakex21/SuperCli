# Delegation appearance restored from v1.0.14

The reference is the most recent released Git tag, `v1.0.14`. The stylesheet and
transcript renderer were extracted byte-for-byte into the portable test stage.
Existing historical delegation screenshots were also inspected. Only the
presentation is restored; the preceding async routing, canonical activity
journal, history pairing and model-context isolation fixes remain in place.

The visible regression was an additional `Output` summary underneath every
activity row and forced line breaks in the delegated brief. It inflated three
ordinary collapsed tool rows from 20 to 53 pixels each. Whole activity rows now
act as the native expandable control, preserving the released single-line
layout and hover result preview. Arguments and results remain available on
click; closing them restores the original height. The brief again uses the
release's normal whitespace presentation without changing its actual text.

Legacy names-only rows use a compact dash with a translated accessible label
and tooltip rather than claiming a successful result. Their limitation remains
available on hover instead of adding a permanent diagnostic paragraph. Known
successes and failures keep their recorded status. Existing UI translations
remain, including the translated default delegation title.

## Measured comparison

The same three real-shaped tool progress/result events and completed report
were rendered by the released and current renderer in a native browser with
their corresponding stylesheets. This uses no external model.

| Measurement | Released v1.0.14 | Before correction | Restored |
| --- | ---: | ---: | ---: |
| Each collapsed activity row | 20 px | 53 px | 20 px |
| Entire expanded delegation card | 251.64 px | 370.02 px | 251.64 px |

Measured horizontal positions of the dot, translated name, hint and status also
match the release. The test additionally opens/closes the actual result and
checks its content and restored height. In ordinary CI it verifies the compact
layout and interaction without depending on a Git tag being available. An
explicit local reference directory enables the additional old/new comparison.

## Verification and deployment

- Release comparison test: passed; both screenshots inspected.
- Actual async Engine-to-browser, history, live status, continuation, language
  and worker ownership regressions: passed.
- Full-shell delegation checks: 93 assertions and 6 screenshots, with no
  overflow at 360, 700 or 1250 px.
- Native UI suite: 558 passed, no skipped or failed tests.
- `go vet ./internal/webgui`, `go build ./...`, both executable builds and
  scoped `git diff --check`: passed.

An initial UI-suite run exposed the small test DOM's lack of `replaceWith`.
Updating the existing status text node both preserves its identity and avoids
that unnecessary operation. The identical full verification sequence then
passed. Earlier fixture setup and failed comparison runs remain in the logs.

The standard CLI/GUI executables and the two previously linked alternate names
were updated with checksum-verified copies. Previous binaries, byte-exact
reference files, comparison screenshots and test logs remain under
`.tmp/worker-release-layout-2026-10-19/`. No existing user data, model prompt,
reasoning setting or API request was changed. No commit or release was made.
