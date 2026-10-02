# Sciter GUI experiment — 2026-10-01

Sciter Direct2D x64 SDK revision 2a890cde76e178cd159fbfbe696cc0959621e4b7 (6.0.5.1), SHA-256 f9edb2ac98cfdf10c6f0a1e14c74b46da553c62b5ef66f4ce8a6001e5a9cf5f2. An isolated launcher reuses SuperCli's Go engine, routes, embedded frontend and provider implementations. Sciter-specific changes are outside the production launcher. The runtime and data remain adjacent to the trial executable. There are no new Go dependencies.

Both final smoke reports passed eleven checks: engine echo, model display, Unicode/progressive SSE, abort, provider presets, appearance, language, remembered runtime, history, submit events and main layout. The streaming fixture split the character ż and delivered its terminal frame about 350 ms after the message; Sciter delivered the message before the terminal frame. This is a transport test, not an LLM throughput benchmark. The final native-programmatic click differed from the browser and was tested through explicit DOM event dispatch. Real pointer/keyboard interaction remains unverified.

| Final sample, entire process tree | Sciter | WebView2 |
| --- | ---: | ---: |
| Private resident memory (MiB) | 56.6 | 65.6 |
| Working set, including shared pages (MiB) | 108.7 | 172.9 |
| Private committed memory (MiB) | 328.1 | 298.7 |
| Idle CPU during fixed 4 s interval (ms) | 15 | 63 |
| First fixture message / terminal (ms) | 91 / 442 | 81 / 432 |

Other visible-window runs put WebView2 private resident memory around 135–142 MiB; Sciter stayed about 57–63 MiB after the broader checks. The variation is large enough that a single sample is not a universal performance claim. Windows can trim working sets. The smaller private working set does not mean lower private committed memory. Metrics exclude the PNG allocation and include the Go backend and all descendant processes. The WebView2 control explicitly shows its window, because a hidden launch otherwise produces a misleadingly small working set.

The stripped launcher plus Sciter runtime is about 30.3 MiB; production supercli-web.exe is about 21.2 MiB. This design reduces browser-runtime dependence but increases package size. Prompt processing still uses exactly the same provider path, including OpenCode Zen.

Reproduction, machine-readable reports and limitations: [Sciter preview](../../experiments/sciter/README.md).

Production fix found by the comparison: providers section rendering now returns its promise. Before this fix, waiting for the section returned before its request completed, and the shared panel completion guard could miss late content after a tab switch.
