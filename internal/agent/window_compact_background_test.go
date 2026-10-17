package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

func waitCompactTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction blocked")
	}
}

func speculativeBand(t *testing.T, l *Loop) {
	t.Helper()
	est := l.nextRequestTokenEstimate().Effective
	window := est + est/10 + minContextReserve
	l.windowFor = func(string) int { return window }
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	if th := autoCompactThreshold(window); est > th || float64(est) < speculativeCompactFrac*float64(th) {
		t.Fatalf("fixture outside speculative band: estimate=%d threshold=%d", est, th)
	}
}

func TestBackgroundCompactGrownPrefixPreservesSuffix(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "manual"}[manual], func(t *testing.T) {
			var calls atomic.Int32
			l := newSpeculativeLoop(t, func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
				calls.Add(1)
				if len(msgs) != 2 {
					return "", errors.New("unexpected prefix")
				}
				return WrapCompactSummary("Goal: old. Done: retained evidence."), nil
			})
			l.maybeStartSpeculativeCompact(context.Background())
			waitCompactTest(t, l.speculative.done)
			suffix := append([]llm.Message(nil), l.Messages[2:]...)
			suffix = append(suffix, llm.Message{Role: llm.RoleAssistant, Content: "completed current"}, llm.Message{Role: llm.RoleUser, Content: "new active task"})
			l.Messages = append(l.Messages[:2], suffix...)
			l.maybeStartSpeculativeCompact(context.Background())
			if manual {
				if _, err := l.CompactNow(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				l.windowFor = func(string) int { return 100 }
				if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
					t.Fatal(err)
				}
			}
			visible := l.VisibleMessages()
			if calls.Load() != 1 || len(visible) != len(suffix)+1 || !reflect.DeepEqual(visible[1:], suffix) {
				t.Fatalf("shorter prefix lost suffix or double paid: calls=%d visible=%+v", calls.Load(), visible)
			}
		})
	}
}

func TestBackgroundCompactReadyConsumedBeforeThreshold(t *testing.T) {
	var calls atomic.Int32
	l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		calls.Add(1)
		return WrapCompactSummary("Goal: old. Pending: continue."), nil
	})
	speculativeBand(t, l)
	l.maybeStartSpeculativeCompact(context.Background())
	waitCompactTest(t, l.speculative.done)
	if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || l.speculative != nil || !IsLegacyCompactionSummary(l.VisibleMessages()[0]) {
		t.Fatalf("ready result not consumed early: calls=%d", calls.Load())
	}
	// No paid call for just the saved summary, even on repeated steps.
	for i := 0; i < 3; i++ {
		if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("normal path extra summaries: %d", calls.Load())
	}
	// After consumption, genuinely new completed history can speculate again.
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "done"}, llm.Message{Role: llm.RoleUser, Content: "next"})
	l.maybeStartSpeculativeCompact(context.Background())
	if l.speculative == nil {
		t.Fatal("speculation did not rearm after consumption")
	}
	waitCompactTest(t, l.speculative.done)
	l.stopSpeculativeCompact()
	if calls.Load() != 2 {
		t.Fatalf("rearmed calls=%d", calls.Load())
	}
}

func TestBackgroundCompactBudgetDoesNotWait(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	l := newSpeculativeLoop(t, func(ctx context.Context, _ llm.Provider, _ []llm.Message) (string, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return WrapCompactSummary("Goal: old. Done: evidence."), nil
	})
	defer l.stopSpeculativeCompact()
	l.modelID = "m"
	l.contextProvider = "conn"
	l.prefillProfiles = llm.LoadPrefillProfiles(t.TempDir())
	l.prefillProfiles.Observe("conn", "m", llm.PrefillSample{InputTokens: 40000, TTFT: 30 * time.Second})
	l.Messages[0].Content += strings.Repeat("x", 70000)
	est := l.nextRequestTokenEstimate().Effective
	hard := autoCompactThreshold(l.window())
	budget, _ := l.effectiveCompactThreshold(hard, "test")
	if est <= budget || est > hard {
		t.Fatalf("bad budget fixture: est=%d budget=%d hard=%d", est, budget, hard)
	}
	before := cloneCompactPrefix(l.Messages)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
			t.Error(err)
		}
	}()
	waitCompactTest(t, done)
	waitCompactTest(t, started)
	if !reflect.DeepEqual(l.Messages, before) {
		t.Fatal("unfinished job mutated history")
	}
	for i := 0; i < 3; i++ {
		if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("budget path duplicate calls=%d", calls.Load())
	}
	close(release)
	waitCompactTest(t, l.speculative.done)
	if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !IsLegacyCompactionSummary(l.VisibleMessages()[0]) {
		t.Fatal("budget did not consume ready result")
	}
}

func TestBackgroundCompactNoRestartChurn(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "growth", true: "edit"}[edit], func(t *testing.T) {
			var calls atomic.Int32
			l := newSpeculativeLoop(t, func(ctx context.Context, _ llm.Provider, _ []llm.Message) (string, error) {
				calls.Add(1)
				<-ctx.Done()
				return "", ctx.Err()
			})
			l.maybeStartSpeculativeCompact(context.Background())
			job := l.speculative
			for i := 0; i < 10; i++ {
				if edit {
					l.Messages[1].Content += "."
				} else {
					l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "done"}, llm.Message{Role: llm.RoleUser, Content: "next"})
				}
				l.maybeStartSpeculativeCompact(context.Background())
				if l.speculative != job {
					t.Fatal("small change restarted summary")
				}
			}
			l.stopSpeculativeCompact()
			if calls.Load() != 1 {
				t.Fatalf("restart waste: %d calls", calls.Load())
			}
		})
	}
}

func TestBackgroundCompactHardAndManualWaitSingleFlight(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "hard", true: "manual"}[manual], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			l := newSpeculativeLoop(t, func(ctx context.Context, _ llm.Provider, _ []llm.Message) (string, error) {
				calls.Add(1)
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return "", ctx.Err()
				}
				return WrapCompactSummary("Goal: old. Done: evidence."), nil
			})
			defer l.stopSpeculativeCompact()
			l.maybeStartSpeculativeCompact(context.Background())
			waitCompactTest(t, started)
			l.windowFor = func(string) int { return 10000 }
			// One large active-turn tool result crosses the hard boundary.
			l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("tool output ", 6000)})
			active := l.Messages[len(l.Messages)-2:]
			done := make(chan struct{})
			go func() {
				defer close(done)
				var err error
				if manual {
					_, err = l.CompactNow(context.Background())
				} else {
					err = l.maybeAutoCompact(context.Background(), nil, "")
				}
				if err != nil {
					t.Error(err)
				}
			}()
			// The channel-controlled helper cannot finish before release.
			select {
			case <-done:
				t.Fatal("hard/manual compaction did not wait")
			default:
			}
			close(release)
			waitCompactTest(t, done)
			visible := l.VisibleMessages()
			if calls.Load() != 1 || !reflect.DeepEqual(visible[len(visible)-2:], active) {
				t.Fatal("duplicate helper or changed active turn")
			}
		})
	}
}

func TestBackgroundCompactFailureNotPaidTwice(t *testing.T) {
	var calls atomic.Int32
	l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		calls.Add(1)
		return "", errors.New("helper unavailable")
	})
	speculativeBand(t, l)
	l.maybeStartSpeculativeCompact(context.Background())
	waitCompactTest(t, l.speculative.done)
	for i := 0; i < 3; i++ {
		if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("failed summary paid twice: %d", calls.Load())
	}
}

func TestBackgroundCompactOversizedRequestNotSent(t *testing.T) {
	l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		t.Fatal("active turn summarized")
		return "", nil
	})
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("active task ", 10000)}}
	l.windowFor = func(string) int { return 4096 }
	if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CompactNow(context.Background()); err == nil {
		t.Fatal("manual compact summarized active turn")
	}
	if err := l.maybeAutoCompact(context.Background(), nil, "context-limit"); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := l.completeOnce(context.Background(), l.prepareToolRequest(), nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds context window") {
		t.Fatalf("oversized request not blocked: %v", err)
	}
}
