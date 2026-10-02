# Head/tail preview without an output-sized copy — 2026-10-02

Parent source: 9fab896. Counting omitted newlines converted the entire omitted string into a mutable byte slice before bytes.Count. The actual Windows/amd64 Go1.26.2 benchmark confirms this allocation remains; replacing that count with strings.Count preserves the same exact LF count without conversion. The production change is one expression, with no cache or protocol changes.

Matched-source same-process, three 100ms samples (Ryzen7 5800X3D):

| Input | Before median | Candidate median | Before allocated B/op | Candidate allocated B/op |
|---|---:|---:|---:|---:|
|64KiB|7.794us|1.576us|70,545|4,948|
|1MiB|94.159us|15.939us|1,054,155|4,949|
|16MiB|1.296ms|0.234ms|16,783,028|4,949|

The public production benchmark after the change separately confirms about 4.95KB/op for all three inputs. This removes temporary allocation proportional to the omitted middle. It does not change output, input tokens, model prefill, or retained application RSS. The input still must be scanned to count its newlines, and producing the bounded preview still allocates. Large synthetic inputs establish scaling, not the frequency of 16MiB outputs in saved sessions. Real failed ctx_execute stream rendering and generic output/worker previews use this helper; their existing caps remain.

All core, ctxexec and workflow tests pass. A private legacy-reference differential overlay verifies exact result bytes for 20,000 deterministic inputs with arbitrary bytes, injected LF and varied cuts. Existing UTF-8, line-boundary, omitted-line and buffer tests remain unchanged. Durable BenchmarkHeadTailLargeOutput covers 64KiB/1MiB/16MiB without timing input creation.

Private benchmark, overlay and logs: .tmp/goal-output-round6-2026-10-02. No provider settings, history, saved outputs, or OpenCode Zen path were changed.
