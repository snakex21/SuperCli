# TUI completed history append rendering

Date: 2026-10-03. Baseline: main 60f4e91 (dev12).

Completed messages already remained cached during streaming, but each appended operational event invalidated the whole completed renderer. Appending one short tool result therefore repeated Markdown, ANSI wrapping, markers and gutters for every archived row. The existing generated benchmark demonstrates this cost at 60, 600 and 2400 mixed completed messages.

The renderer can now extend the existing completed string only when the previously rendered messages, width, language, legacy-symbol mode, thinking/tool folds, palette and terminal color environment are unchanged. A cloned immutable snapshot of all msg headers detects mutations through shared copied-model backing arrays. Only the exact built-in NewPalette qualifies; custom styles and transforms retain the full renderer and callback ordering. Removed/folded/edited messages or changed rendering inputs force a complete rebuild. Empty history and reset release the snapshot. No raw text is truncated, no history is evicted, and no provider, prompt, instruction, tool or Zen route changes.

## Measurement

Portable ignored evidence: .tmp/goal-tui-history-round10-2026-10-03. Same existing BenchmarkTUICompletedHistoryAppend fixture and generated addToolResult→refreshTranscript→SetContent→View fixture; Windows amd64, Go 1.26.2, Ryzen 7 5800X3D, no-color palette, width100, terminal viewport100×36. ABBA, 20 iterations per case, no model or real terminal process. Copied input message slices are prepared outside timing in both variants. Numbers are completed receipts, not progress samples.

| Completed messages | Completed renderer before | After | Full refresh + View before | After | Full path transient B/op before | After |
|---:|---:|---:|---:|---:|---:|---:|
|60|3.25–3.47ms|77–85µs|3.62–4.23ms|0.376–0.378ms|1.447–1.455MB|0.207MB|
|600|35.40–37.86ms|135–148µs|34.77–39.73ms|1.748–1.759ms|14.854–14.884MB|0.855–0.862MB|
|2400|132.35–143.78ms|305–364µs|131.65–150.13ms|6.357–6.484ms|59.010–59.021MB|3.008–3.012MB|

At 2400 messages completed-only allocations drop 338213–338218→60; full refresh/View 338363–338366→205. The unchanged completed renderer still allocates zero. Decimal MB above denotes temporary Go allocations per operation, not process RSS. The full fixture includes viewport splitting and View; it excludes terminal repaint, provider generation, stream transport and OS input latency. The mixed archive is generated stress data, not an estimate of typical conversations.

The optimization has an explicit residency cost: one immutable snapshot with a 21632-byte fixed structure and 64-byte msg headers on this architecture. At 60/600/2400 messages that totals 25472/60032/175232 bytes. String headers reference existing source bytes and the existing rendered prefix; neither raw message bodies nor completed rendered bytes are duplicated by the snapshot. Copied models share that immutable object until publishing their own new version. Dirty append still compares all previous headers and allocates the newly assembled complete transcript string; it does not make history processing constant-time or eliminate viewport scanning. Custom palettes do not receive this prefix optimization.

## Correctness

The independent previous full renderer passes both baseline and candidate controls. Generated sequences cover 27 supported languages, five widths (1,3,5,17,80), ASCII/ANSI256, Unicode/graphemes, thinking, structured tool results/errors, documents, individual fold, tool/thinking fold, removal, width/language/symbol changes, terminal dark/profile changes, stream flush, empty/reset and custom padding. Shared-message mutation, divergent copied appends, shrinking and stateful callback order/range semantics have separate controls. A durable ownership check confirms copied-model snapshots remain immutable, empty/reset releases history and custom styles do not publish reusable snapshots.

Candidate overlay full TUI tests passed in 4.774 s; final production full TUI tests and vet pass (receipts production-tests.txt and production-vet.txt). Independent agent review found no blocker and confirmed lipgloss 1.1 styles hold stock properties/colors by value. Full application integration and cross-builds are recorded separately.

The gofmt-normalized measured renderer and helper are byte-identical to the final production port; measured-production-equivalence.json records their hashes.
