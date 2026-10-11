# SuperCli's pinned glaze backend

The root production package is copied from `github.com/crgimenes/glaze` v0.0.31.
The original MIT license is preserved in LICENSE. Unused standalone subpackages
and examples are omitted; the root package does not import them. Upstream's test
harness is omitted: it automatically starts native windows before tests can
configure isolated portable profiles. Local COM tests use explicit isolation.

Local extension: `memory_target_windows.go` exposes
`SetMemoryUsageTarget(low bool) error` on the Windows implementation. Calls must
run on the WebView's UI thread. The cross-platform WebView interface is unchanged.

The COM interface identifier and vtable slots are taken from Microsoft's
WebView2 SDK 1.0.1901.177, `build/native/include/WebView2.h`:
ICoreWebView2_19, IID 6921F954-79B0-437F-A997-C85811897C68;
getter slot 119, setter slot 120 (zero-based, including IUnknown).
NORMAL = 0, LOW = 1. QueryInterface's acquired reference is released after
each call. Runtimes without this interface return a distinct unsupported error.

API contract:
https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2_19

When updating glaze, compare this copy against the new release, preserve this
extension or use an equivalent upstream API, and rerun its COM tests and the
real GUI minimize/restore measurement. Do not infer native COM pointers from
private Go struct offsets or edit the Go module cache.
