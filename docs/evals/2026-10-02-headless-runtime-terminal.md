# Headless integration, terminal input and bounded runtime costs (2026-10-02)

The other-agent branch origin/fix/run-scoped-agent-guidance (27e6736) is already an ancestor of main c5afaf8. Fetch and a read-only branch comparison found no unique commits to merge.

## Changes and evidence

- Local QMP and WebDriver adapters load through one discoverable tool. Constructor/registration do not start a program, create a profile, dial a socket, or add full schemas to ordinary turns. Explicit QMP/headless/WebDriver requests expose the adapter and existing process_session contract for this run only; replay tests exercise direct and invoke_tool calls in two provider requests (one action, one result continuation), without a discovery request.
- QMP supports query-status, an ordered complete key chord, normalized absolute pointer clicks, original PNG screenshots and blocking event waits. One requested event received during capability negotiation is retained. Endpoint gates serialize one target while remaining independent for different targets; canceled transports disconnect but never terminate an attached VM.
- WebDriver opens an explicitly headless Chrome/Firefox session with an app-local profile, or addresses an existing session. Bounded visible DOM semantics with unique selectors reduce the need for pixel inference. Screenshots use the existing analysis budget and automatic lazy GUI previews. Canceled navigation retains the newly created session ID in its error so a retry can reuse/close the session rather than launch another browser. Endpoint spellings are canonicalized for locking and profile identity.
- The process output cap previously appended then recopied the retained 64-KiB tail on every overflow. Its circular buffer now preserves the same bytes/cursors/omission counts without steady-state write allocations. For 32-KiB writes, a CPU microbenchmark measured 12.775 us / 172,032 allocated bytes / 2 allocations versus 0.526 us / 0 bytes / 0 allocations. Quiet buffers remain lazily allocated. This measures the output buffer, not provider latency or total process RSS.
- Managed processes can explicitly opt into a lifetime up to 24 hours for long headless work. The default remains ten minutes and the active-process cap remains three. Milliseconds are clamped before duration conversion to avoid integer-overflow shortening.
- GUI older-page loading prepends just the older fragment. It preserves active streamed messages, expanded tool results and latest worker backlinks rather than rebuilding the complete transcript. The 60+60-row fixture creates 360 instead of 720 summary elements.
- Ordinary TUI typing skips unchanged input/viewport layouts. The measured type/backspace pair improved from about 88 us to 54 us; multiline, attachment and autocomplete geometry regressions pass. This does not change provider inference time.
- First-run provider selection created a fresh adaptive renderer for every view. Its terminal color queries could read the same terminal as Bubble Tea and leak cursor-position reports such as ESC[29;1R and ESC[32;1R into search. One palette is now prepared before the input reader starts and reused. Actual fragmented protocol replies are recovered/discarded, while literal bracket text, paste, navigation, Escape and overlapping Alt/function keys are preserved. Replays use Bubble Tea's real decoder at every byte split. View allocations measured 32.6 KB to 24.3 KB. An actual Linux user's terminal has not been exercised here.

- WebDriver screenshot decoding now reads the usual base64 payload directly from json.RawMessage rather than allocating a full second base64 string. A synthetic 4-MiB CPU benchmark measured 28.27 ms / 9,797,810 B / four allocations versus 17.32 ms / 4,194,380 B / one allocation. Escaped JSON, padding and CR/LF keep the standard-decoder behavior. Exactly 16 MiB is accepted and larger decoded images are rejected before allocating the destination. This measures decoding only, not browser RSS or model inference.

## Grounded neighbor inspiration

DeepSeek's experimental browser-use runtime acquires SessionResources only on its first operation and serializes within an exact owner. Stagehand separates owned launches from attached external browsers. This integration uses lazy endpoints, per-target admission, explicit ownership, and the existing managed process launcher rather than a new daemon.

Pi's coding-agent bash executor retains a bounded moving set of chunks instead of repeatedly rebuilding a complete tail. The Go implementation adopts fixed-capacity circular storage without Pi's system-temp spool, keeping the portable-data contract.

## Boundaries

No guest OS was installed and no real VM disk or user desktop was captured. No QEMU, browser or driver was downloaded or bundled. Protocol tests use loopback fixtures and synthetic images; WebDriver's inspector is exercised by an actual JavaScript DOM fixture. Running a VM/browser still costs that external application's CPU/RAM. Ordinary SuperCli work does not acquire those resources.

QEMU 7.1+ PNG support and a local shared capture directory are required. Existing WebDriver servers/browsers must be installed by the user. See [headless-control.md](../headless-control.md) for launch/control examples and ownership/lifetime details. This is development work, not a new public release.

## Verification

- `go test ./... -count=1 -p 2`: passed all packages.
- `go vet ./...`: passed.
- Complete JavaScript UI suite: 130 passed, zero failed.
- Source SHA-256 guard: unchanged during checks and compilation.
- Ten CGO-disabled builds: TUI and GUI for Windows amd64, Linux amd64/arm64, macOS amd64/arm64. All passed; cross-compilation is not native runtime verification.
- Windows TUI and GUI installed with matching build SHA-256 values. CLI version reports `1.0.4-dev.2`; both help commands exit successfully.
- Portable Linux amd64 development ZIP prepared with both binaries, public documentation and built-in skills. No user configuration/history is bundled and no release/update manifest was published.
- Native Linux terminal and real QEMU/WebDriver execution remain unverified; tests used deterministic loopback/DOM fixtures.
