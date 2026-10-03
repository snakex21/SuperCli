# Deferred session title callback ownership

Date: 2026-10-03. Baseline: a60c4ab / dev21.

## Defect and fix

A GUI session-title timer could already have dispatched its callback when a new message called `titleScheduler.Cancel`. `Timer.Stop` cannot retract an already-dispatched callback. The old `fire` looked up only the session key, so it could start a new helper after cancellation, use an obsolete prompt after rescheduling, or consume a newly created job under the same key. A delayed completion could clear the cancel function of a newer active run or delete/replace its pending timer.

The scheduler now verifies captured job identity and timer generation before creating a context or consuming an attempt. Run generation protects the cancel field. Explicit-schedule generation prevents old completion from deleting or rearming a newer schedule. A delivered callback consumes its pending timer once. `Close` still cancels jobs; the attempt limit and idle delay are unchanged.

Cancel of an **already active** run retains the existing bounded retry after the idle delay. A new explicit Schedule retains cumulative attempts and owns its timer. Existing `MeteredProvider.registerBackgroundWhenIdle` remains responsible for blocking actual provider entry while foreground work is active. This change does not claim that the old scheduler ran model inference concurrently with a foreground stream.

## Deterministic red/green proof

Ignored frozen overlays under `.tmp/goal-harness-dev21-2026-10-03` contain dev21 baseline/candidate source and adapters that capture the exact installed callback inputs. An owned long-delay timer is stopped on manual delivery; channels control completion. The proof uses no sleeps, progress polling, live models, network access, user session data, or app process manipulation.

Root ran the baseline then candidate using `root-proof.cmd` and the existing repository-local Go cache. Frozen source/proof hashes were verified. The auditor prepared but did not execute the proof. The alternate supplied runner was not executed.

| Control | Baseline | Candidate |
| --- | --- | --- |
| Deliver queued callback after Cancel | 1 helper / 1 fake Complete; expected 0 | 0 / 0 |
| Deliver obsolete callback after Schedule | 1 obsolete helper; latest topic lost | 0 obsolete calls; 1 latest-topic call |
| Deliver old callback after same-key job replacement | consumes replacement with old topic | old job rejected; replacement retained |
| Old completion after newer active run | detaches newer cancel owner | newer context remains cancelable |
| Old failure/success after newer pending Schedule | replaces/deletes newer timer | original newer timer retained |
| Close, normal completion, active retry, attempt cap | passes | passes |
| Actual runSessionTitleLLM local/manual/canceled/fallback controls | passes | passes |

The fake provider calls the actual local `Complete` interface and verifies background/title purpose, unchanged two-message shape, and no tools. These are deterministic lifecycle counts, not billable API measurements. Eight test groups are retained as `title_callback_test.go`, including both delayed success and failure. Existing title tests continue to check real timers and bounded active retries.

## Cost and limits

Three generation counters add 24 bytes to each pending job's struct on a 64-bit build, with captured ownership values and comparisons in title callbacks. No extra timers, model instructions, dependencies, per-token operations, or foreground request probes are added. This is a correctness fix with a demonstrated avoidable helper call in the synthetic stale-callback case; real historical incidence, total token savings, latency, and RSS were not measured. TUI does not use this GUI title scheduler. Its history cancellation fix is documented separately.

Workspace-local OpenCode's title lifecycle checks and SuperCli's existing memory-idle timer generation guard informed the audit. No external prompt policy or different model was imported. The OpenCode Zen transport remains unchanged.

## Integrated validation

The final combined source passed the full Go suite (71 tested packages and 28 packages without tests), `go vet ./...`, formatting, and diff checks. Source hashes (1,866 files under cmd/internal/test plus module manifests) stayed identical through checks. No frontend file changed; the prior dev21 run of 194 Node UI tests was retained rather than repeated. Live Qwen/Zen/AnyRouter calls were not used for these DB/callback changes.

All ten CGO-disabled CLI/GUI builds passed for Windows amd64, Linux amd64/arm64, and macOS amd64/arm64. Build source snapshots matched the tested source. Both local Windows EXEs were installed as `1.0.4-dev.22`, with exact old/new hashes and portable backups verified. CLI version/help and GUI target/linked-version checks passed. No user app was closed, activated, or restarted. Cross-platform execution and native GUI rendering were not measured. Artifact/check manifests are under `.tmp/goal-integrated-dev22-2026-10-03`.
