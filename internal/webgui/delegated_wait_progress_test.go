package webgui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
)

type delegatedWaitProgressProvider struct {
	engine                   *Engine
	command, target          string
	stall                    bool
	cancelMode               bool
	parentCalls, workerCalls atomic.Int32
	resumed                  chan struct{}
	workerReleased           chan error
	reportReady, reportGo    chan struct{}
}

func (*delegatedWaitProgressProvider) Name() string { return "delegated-wait-progress-fixture" }

func (p *delegatedWaitProgressProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	parent := false
	for _, def := range defs {
		parent = parent || def.Name == "task"
	}
	if parent {
		switch p.parentCalls.Add(1) {
		case 1:
			return workerSteeringWireCall("spawn-wait-worker", "task", map[string]any{
				"agent": "code", "prompt": "Run the two isolated fixture commands, then report WAIT_REPORT_READY.", "async": true,
			})
		case 2:
			return workerSteeringWireDeltas(llm.Delta{Content: "Waiting for the worker report.", FinishReason: "stop"}), nil
		case 3:
			found := false
			for _, message := range messages {
				found = found || (message.Role == llm.RoleUser && strings.Contains(message.Content, "WAIT_REPORT_READY"))
			}
			if !found {
				return nil, errors.New("coordinator resumed without its worker report")
			}
			close(p.resumed)
			if p.stall {
				return stalledWebProvider{}.Complete(ctx, messages, defs)
			}
			return workerSteeringWireDeltas(llm.Delta{Content: "Worker report applied automatically.", FinishReason: "stop"}), nil
		default:
			return nil, errors.New("coordinator repeated a finished task")
		}
	}
	n := p.workerCalls.Add(1)
	if n <= 2 {
		return workerSteeringWireCall(fmt.Sprintf("wait-command-%d", n), "ctx_execute", map[string]any{
			"command": []string{p.command, "--disable", "--noproxy", "*", "--silent", "--show-error", fmt.Sprintf("%s/%d", p.target, n)},
		})
	}
	if n != 3 {
		return nil, errors.New("worker repeated a finished command")
	}
	if p.cancelMode {
		// Subscribe once to the actual invocation's completion. Cancellation
		// leaves this accepted worker detached, so cleanup must await its
		// lifetime event rather than polling registry status or closing stores.
		manager, err := p.engine.checkpointManager(p.engine.Home())
		if err != nil {
			return nil, err
		}
		turn, leave, err := checkpoint.EnterBoundMutation(ctx, manager, nil, nil)
		if err != nil {
			return nil, err
		}
		leave()
		if _, err := turn.CompleteDeferred(ctx, func(_ *checkpoint.Record, err error) { p.workerReleased <- err }); err != nil {
			return nil, err
		}
	}
	close(p.reportReady)
	out := make(chan llm.Delta, 1)
	go func() {
		defer close(out)
		select {
		case <-p.reportGo:
			out <- llm.Delta{Content: "WAIT_REPORT_READY", FinishReason: "stop"}
		case <-ctx.Done():
		}
	}()
	return out, nil
}

func TestDelegatedHostWaitPausesModelWatchdogAndResumesItAfterReports(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("native curl is unavailable")
	}
	for _, mode := range []string{"complete", "cancel", "stalled-parent-after-report"} {
		t.Run(mode, func(t *testing.T) {
			const modelIdle = 200 * time.Millisecond
			srv := newTestServer(t, false)
			srv.eng.setHome(t.TempDir())
			writeDiagnosticOrchestratorFixture(t, srv.eng.DataDir())
			entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
			release := []chan struct{}{make(chan struct{}), make(chan struct{})}
			var enterOnce, releaseOnce [2]sync.Once
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := 0
				if r.URL.Path == "/2" {
					index = 1
				} else if r.URL.Path != "/1" {
					http.Error(w, "unexpected fixture request", http.StatusNotFound)
					return
				}
				enterOnce[index].Do(func() { close(entered[index]) })
				select {
				case <-release[index]:
					fmt.Fprint(w, "isolated-command-proof")
				case <-r.Context().Done():
				}
			}))
			defer target.Close()
			provider := &delegatedWaitProgressProvider{
				engine: srv.eng, command: curl, target: target.URL, stall: mode == "stalled-parent-after-report", cancelMode: mode == "cancel",
				resumed: make(chan struct{}), workerReleased: make(chan error, 1),
				reportReady: make(chan struct{}), reportGo: make(chan struct{}),
			}
			srv.eng.mu.Lock()
			srv.eng.prov, srv.eng.cfg.Timeout = provider, modelIdle
			srv.eng.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			waiting, externalAfterWait, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var waitOnce, externalOnce, reportOnce sync.Once
			var runErr error
			var terminal bool
			var trace []wireEvent
			go func() {
				defer close(done)
				runErr = srv.eng.runStream(ctx, "Delegate the fixture commands and apply their report.", "", "", func(ev wireEvent) {
					trace = append(trace, ev)
					if ev.Type == "notice" && strings.Contains(ev.Text, "Waiting for delegated tasks") {
						waitOnce.Do(func() { close(waiting) })
					}
					if ev.Type == "worker_progress" && ev.Kind == "tool_call" && ev.CallID == "wait-command-2" {
						externalOnce.Do(func() { close(externalAfterWait) })
					}
					if ev.Type == "finishing" && mode == "complete" {
						// A completed provider cannot make further model progress.
						// Simulate slow host finishing with one blocking timer.
						<-time.After(4 * modelIdle)
					}
					terminal = terminal || ev.Type == "done"
				})
			}()
			defer func() {
				for i := range release {
					releaseOnce[i].Do(func() { close(release[i]) })
				}
				reportOnce.Do(func() { close(provider.reportGo) })
				if provider.workerCalls.Load() > 0 && provider.cancelMode {
					waitCompletionTest(t, provider.reportReady, "worker terminal reply cleanup")
				}
				cancel()
				waitCompletionTest(t, done, "delegated host-wait cleanup")
				if t.Failed() {
					t.Logf("completed trace: %+v; error=%v; parent=%d worker=%d", trace, runErr, provider.parentCalls.Load(), provider.workerCalls.Load())
				}
				if provider.workerCalls.Load() > 0 && provider.cancelMode {
					select {
					case err := <-provider.workerReleased:
						if err != nil {
							t.Errorf("worker invocation cleanup: %v", err)
						}
					case <-time.After(10 * time.Second):
						t.Error("worker invocation did not release its checkpoint")
					}
				}
			}()
			waitCompletionTest(t, waiting, "parent host wait")
			waitCompletionTest(t, entered[0], "first delegated command")
			// Permit a real external worker result/call after the host wait has
			// begun. That activity must not accidentally rearm the paused timer.
			releaseOnce[0].Do(func() { close(release[0]) })
			waitCompletionTest(t, externalAfterWait, "external progress during host wait")
			waitCompletionTest(t, entered[1], "second delegated command")
			select {
			case <-done:
				t.Fatalf("GUI ended while a delegated command was active: %v", runErr)
			case <-time.After(4 * modelIdle):
			}
			releaseOnce[1].Do(func() { close(release[1]) })
			waitCompletionTest(t, provider.reportReady, "worker terminal reply prepared")
			if mode == "cancel" {
				cancel()
				waitCompletionTest(t, done, "cancel during delegated host wait")
				if !errors.Is(runErr, context.Canceled) || provider.parentCalls.Load() != 2 || terminal {
					t.Fatalf("cancel resumed parent or lost cancellation: err=%v calls=%d done=%v", runErr, provider.parentCalls.Load(), terminal)
				}
				return
			}
			reportOnce.Do(func() { close(provider.reportGo) })
			waitCompletionTest(t, provider.resumed, "automatic coordinator continuation")
			waitCompletionTest(t, done, "coordinator terminal response")
			if mode == "stalled-parent-after-report" {
				if runErr == nil || !strings.Contains(runErr.Error(), "provider produced no model progress") || terminal {
					t.Fatalf("watchdog did not resume after reports: err=%v done=%v", runErr, terminal)
				}
			} else if runErr != nil || !terminal {
				t.Fatalf("automatic continuation failed: err=%v done=%v", runErr, terminal)
			}
			if provider.parentCalls.Load() != 3 || provider.workerCalls.Load() != 3 {
				t.Fatalf("unexpected repeated work: parent=%d worker=%d", provider.parentCalls.Load(), provider.workerCalls.Load())
			}
		})
	}
}
