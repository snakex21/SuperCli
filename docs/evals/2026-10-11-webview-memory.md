# WebView2 memory target on minimize

SuperCli now requests WebView2's `MemoryUsageTargetLevel=Low` when its native
Windows window is minimized and restores `Normal` when the window is restored
or maximized. The existing window procedure delivers these events on the COM
UI thread. Ordinary resize events are deduplicated; no timers or progress
polling are added. Unsupported runtimes keep the existing behavior.

The API keeps scripts and network connections running. Reduction is best
effort and can include paging to disk; restoring Normal does not force every
unused page back into RAM immediately. This optimizes inactive GUI memory,
without changing model settings, prompts, conversation state or compaction.

## Backend integration

Glaze v0.0.31 does not expose this API. `third_party/glaze` contains its pinned
root production package and the original MIT license, with a small Windows
extension. All 19 copied upstream files were verified byte-for-byte against
the original module. The copy, extension and tests total approximately 162 KB.

The extension queries ICoreWebView2_19, calls the setter and releases the acquired
COM reference after each call. The IID, enum values and slots were checked
against Microsoft's WebView2 SDK 1.0.1901.177 header. No private Go struct
offsets, reflection, linkname or module-cache edits are used.

## Full process-tree measurement

Two rounds were run in A/B then B/A order, with separate portable test profiles
and projects, the real GUI frontend, and the Echo provider. Readiness waits for
both GUI initialization and a test SSE connection. Measurements occur once at
each predetermined phase boundary: 6 seconds visible, 30 seconds minimized,
6 seconds restored. There are no progress queries between boundaries.

All seven processes were included: Go host, browser, crash handler, GPU,
two utilities and renderer. Values below are MiB (1,048,576 bytes).
Resident figures use psutil's USS; private bytes measure committed allocation.
Summed working sets are also recorded but can double-count shared pages.

| Build / round | Resident visible | Resident minimized | Resident restored | Private bytes visible → minimized | Available system RAM visible → minimized (GiB) |
| --- | ---: | ---: | ---: | ---: | ---: |
| Baseline 1 | 228.5 | 221.7 | 232.5 | 273.2 → 265.0 | 23.31 → 22.90 |
| Memory API 1 | 217.6 | 120.0 | 107.6 | 264.0 → 249.8 | 22.77 → 22.76 |
| Memory API 2 | 217.7 | 118.7 | 104.7 | 271.0 → 255.1 | 22.52 → 22.20 |
| Baseline 2 | 227.2 | 45.6 | 92.5 | 264.4 → 255.1 | 21.52 → 25.92 |

The new build returned about 98–99 MiB of resident memory within each of its
two runs. It freed much less committed memory. These observations are not a
guaranteed saving relative to the old build: Windows also trimmed the final
control run while system available memory changed by about 4.4 GiB. Other
system activity, including the concurrent VM work, can affect the result;
its exact contribution was not measured. No clean aggregate A/B percentage
is claimed.

All four final runs completed successfully. JavaScript/SSE acknowledgements
arrived while minimized and after restoration. A restoration acknowledgement
also waits for `requestAnimationFrame`; it arrived in 3.3/3.7 ms for the new
build and 1.9/1.9 ms for the baseline after the settling interval. This checks
continued scripting and drawing, not time from the restore command to first
paint. All windows closed with exit code 0.

Earlier exploratory measurements waited for document load rather than complete
GUI initialization. Some failed readiness/minimization checks; they are not
included in the table or averaged into the result.

## Browser flags

No GPU, renderer-limit or disable-features flags were added to production.
The exploratory environment-variable trial still had a separate GPU process.
The test host was elevated; Microsoft documents that elevated hosts ignore
environment browser flags. That trial therefore does not establish whether
those switches save memory. Microsoft also advises against shipping these
unsupported switches in production. SmartScreen-disabling flags were not used.

## Validation and installation

- Root `go test -p 1 -count=1 ./...`: all 76 tested packages passed.
- Root `go vet -p 1 ./...` and `go build -p 1 ./...`: passed.
- Pinned backend tests and vet: passed, including a real runtime getter check
  after repeated Low/Normal transitions, unsupported-interface handling,
  failed setter calls and COM reference release.
- Window policy tests cover minimize/restore/maximize, repeated resize events,
  unavailable runtimes and retry after a transient failure.
- Validation hashed 2,184 Go/module inputs and checked them before installation.
- Both CLI and GUI were rebuilt and installed with verified local backups.
  The installed GUI SHA-256 equals the measured candidate:
  `6BD75255F893A60454CB463BCC63F9608883D1B39BBBE79036CE9618DB0F269B`.
- Python benchmark syntax and command-line help passed. A restart is required
  for an already running GUI to load the new binary.

The first exploratory backend test inadvertently ran upstream's automatic GUI
TestMain before profile isolation, touching its pre-existing AppData test
profile. That harness is excluded from the pinned copy. Subsequent native
tests and all benchmark cases use explicit portable profiles; the pre-existing
external profile was not deleted.

Local receipts are under `.tmp/webview-memory-2026-10-11/`: validation,
native-COM test output, `measurements-api-30s.json`, build/install manifests
and backups. The durable benchmark is `scripts/measure_webview_memory.py`.
With Python and psutil available, repeat from the repository root:

```powershell
python scripts/measure_webview_memory.py --baseline .tmp/webview-memory-2026-10-11/baseline/supercli-web.exe --candidate .tmp/webview-memory-2026-10-11/candidate/supercli-web.exe --output-dir .tmp/webview-memory-repeat --minimized-seconds 30 --rounds 2
```

## Primary references

- [Microsoft: ICoreWebView2_19](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2_19)
- [Microsoft: browser flags and elevated hosts](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/webview-features-flags)
- [Microsoft WebView2 SDK 1.0.1901.177](https://www.nuget.org/packages/Microsoft.Web.WebView2/1.0.1901.177)
- [Upstream glaze](https://github.com/crgimenes/glaze)
