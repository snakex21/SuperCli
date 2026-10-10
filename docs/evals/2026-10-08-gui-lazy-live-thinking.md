# Folded live reasoning defers Markdown work

When a user saved `thinkingExpanded=false` or folded an active reasoning card,
`assistantPart` still built its Markdown DOM and updated it on every paced render.
Only initially folded history had a lazy first render. The GUI now defers live and
historical thinking while the card is closed, including after it was opened once.
Opening paints the latest received section; repeated reopening preserves the
existing DOM. The toggle listener is removed when its assistant part is discarded.

The full transcript, source parser, pacing and semantic boundaries remain intact.
Live reasoning still opens by default unless the user saved a folded preference.
This change does not release a DOM that the user already opened, remove raw text,
limit reasoning, or change model calls and prompt instructions.

## Validation

The regular dependency-free Node UI gate includes
`test/ui/live-thinking-lazy.test.cjs` and its local DOM fixture. It exercises the
actual event handler, parser, Markdown stream, assistant renderer, keyboard handler
and history builder using synthetic plain paragraphs and Unicode. It checks:

- No hidden Markdown initialization/update before first reveal or while refolded.
- Exact received reasoning and answer after `done`, SSE error, tool-call boundary,
  EOF seal, and repeated flush/seal; no pending animation callbacks.
- Stable answer-node identity and thought ordering for late and usage-only reasoning.
- Summary click and keyboard reopening with the saved disclosure preference.
- Direct recovery source replacement, removal of the old part and listener cleanup.
- Retained source and successful retry after an injected deferred-formatter error.
- Folded/open history, default disclosure preferences and repeat opening.

The existing `reasoning-order` regression now checks that folded hidden DOM stays
unchanged, then requires the exact latest thought after reopening. Its answer-node,
order, raw-source and pending-frame assertions remain.

Run the portable dependency-free gate with:

```sh
node scripts/test-ui.cjs
```

Result on 2026-10-08: **215 passed, 0 failed**, Node v24.19.0. Go and native
application checks are delegated to the root validation pass.

The local fixture models browser summary activation and explicitly delivers the
resulting `toggle` event. It is not an HTML parser or layout engine. Rich Markdown
parity was additionally checked using the existing optional Node + LinkeDOM
transcript fixture in the isolated `.tmp/optimization-oct8-2026/gui-live-thinking`
experiment. No new production or test-gate dependency was introduced.

## Isolated experiment

Node v24.19.0, actual `handleEvent`, `renderAssistant` and `updateMarkdownStream`,
64 chunks of 74 UTF-16 characters, arrivals every 48 ms interleaved with requested
16 ms animation frames. CPU timing remains part of production pacing. Results are
medians of three runs after warmup; first reveal is timed separately.

| Work | Baseline | Lazy thinking |
| --- | ---: | ---: |
| Folded live handler/render time | 35.343 ms | 1.847 ms |
| Folded live `renderAssistant` calls | 188 | 188 |
| Folded live `updateMarkdownStream` calls | 188 | 0 |
| Cumulative Markdown input characters while folded | 444,994 | 0 |
| Hidden content elements before first reveal | 129 | 0 |
| First reveal after the folded stream | 0.045 ms | 0.945 ms / 1 update |
| Expanded live handler/render time | 30.509 ms | 26.623 ms |
| Expanded live render/Markdown update calls | 188 / 188 | 188 / 188 |

The expanded timing difference is not evidence of a speedup. The confirmed change
is elimination of hidden formatting and initial hidden DOM construction, while
the same paced render callbacks remain. Raw source and canonical revealed DOM
hashes matched. The optional experiment also checked rich inline Markdown and a
table, history restoration, source recovery and the terminal/reopening paths.

LinkeDOM requires a fixture-only shim for stable `template.content` identity: it
otherwise refills an emptied fragment from stale children, unlike browser DOM.
The shim was applied equally to baseline and candidate; no runtime code or
dependency was modified. Baseline snapshot SHA256:
`2536a2f8effb3dfacc10b9456736623056647be831c77f806aa22b8612232c4e`.
The snapshot remains unchanged. An independent adoption check confirmed the
production function matches the measured overlay and all other transcript
implementation changes from the baseline are preserved.

These timings do not measure application RAM, browser FPS, geometry, layout, GPU,
WebView2, or model latency. Natural browser toggle delivery and native rendering
were not exercised. Deferred formatter exceptions retain the existing history
reveal behavior: the event exception can surface, while source remains available
for a later reopening/retry. Normal SSE error finalization is covered separately.
No Go execution, application, live model, network or user conversation was used.
