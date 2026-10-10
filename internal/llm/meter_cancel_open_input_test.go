package llm

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// This producer intentionally holds its output open after cancellation. Only
// release ends it, so the wrapper cannot pass by waiting for provider EOF.
type meterCancelOpenProducer struct {
	deltas  []Delta
	waiting chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (p *meterCancelOpenProducer) Name() string { return "open-input-test" }

func (p *meterCancelOpenProducer) Complete(ctx context.Context, _ []Message, _ []ToolDef) (<-chan Delta, error) {
	out := make(chan Delta)
	go func() {
		defer close(p.done)
		defer close(out)
		for _, d := range p.deltas {
			select {
			case out <- d:
			case <-ctx.Done():
				<-p.release
				return
			}
		}
		close(p.waiting)
		<-p.release
	}()
	return out, nil
}

func waitMeterCancelSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func TestMeteredCancellationFinishesBeforeProducerEOF(t *testing.T) {
	for _, tc := range []struct {
		name   string
		deltas []Delta
		usage  Usage
	}{
		{name: "before_output"},
		{
			name: "after_usage",
			deltas: []Delta{
				{Content: "partial answer"},
				{Usage: &Usage{Input: 20, Output: 3, Total: 23, CachedInput: 7, Reasoning: 2}},
				{Notice: "accepted usage barrier"},
			},
			usage: Usage{Input: 20, Output: 3, Total: 23, CachedInput: 7, Reasoning: 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			producer := &meterCancelOpenProducer{
				deltas: tc.deltas, waiting: make(chan struct{}),
				release: make(chan struct{}), done: make(chan struct{}),
			}
			wrapperStats, contextStats := &sinkCapture{}, &sinkCapture{}
			ctx, cancel := context.WithCancel(WithCallSink(context.Background(), contextStats.sink()))
			t.Cleanup(func() {
				cancel()
				close(producer.release)
				waitMeterCancelSignal(t, producer.done, "producer cleanup")
			})
			provider := Metered(producer, "test", PurposeMain, wrapperStats.sink())
			stream, err := provider.Complete(ctx, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.deltas {
				select {
				case got, ok := <-stream:
					if !ok || !reflect.DeepEqual(got, want) {
						t.Fatalf("delta = %+v, open=%v; want %+v", got, ok, want)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("wrapper did not relay scripted delta")
				}
			}
			waitMeterCancelSignal(t, producer.waiting, "producer waiting without EOF")
			cancel()
			select {
			case d, ok := <-stream:
				if ok {
					t.Fatalf("unexpected delta after cancellation: %+v", d)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Metered waited for producer EOF after cancellation")
			}

			// Both sinks run before wrapper EOF, once, with already accepted usage.
			wrapped, outer := wrapperStats.all(), contextStats.all()
			if len(wrapped) != 1 || len(outer) != 1 || !reflect.DeepEqual(wrapped, outer) {
				t.Fatalf("cancellation stats: wrapper=%+v context=%+v", wrapped, outer)
			}
			stat := wrapped[0]
			if !stat.Canceled || stat.Failed || stat.Background || stat.Purpose != PurposeMain || stat.Model != producer.Name() {
				t.Fatalf("cancellation labels = %+v", stat)
			}
			if stat.TokensIn != tc.usage.Input || stat.TokensOut != tc.usage.Output ||
				stat.TokensCached != tc.usage.CachedInput || stat.TokensReasoning != tc.usage.Reasoning {
				t.Fatalf("accepted usage lost or estimated: %+v; want %+v", stat, tc.usage)
			}

			// A new background Complete cannot enter until the foreground marker
			// is released. Its entry proves cleanup without releasing the producer.
			bgCtx, bgCancel := context.WithTimeout(WithBackground(context.Background()), 2*time.Second)
			defer bgCancel()
			bgStarted, bgRelease := make(chan struct{}, 1), make(chan struct{})
			close(bgRelease)
			background := Metered(&holdStub{name: "background-after-stop", started: bgStarted, release: bgRelease}, "test", "memory", (&sinkCapture{}).sink())
			bgDone := make(chan error, 1)
			go func() {
				bgStream, err := background.Complete(bgCtx, nil, nil)
				if err == nil {
					for range bgStream {
					}
				}
				bgDone <- err
			}()
			waitMeterCancelSignal(t, bgStarted, "background entry after foreground cancellation")
			select {
			case err := <-bgDone:
				if err != nil {
					t.Fatalf("background remained blocked: %v", err)
				}
			case <-bgCtx.Done():
				t.Fatal("background did not finish after foreground cancellation")
			}
			select {
			case <-producer.done:
				t.Fatal("test producer reached EOF before wrapper cleanup was verified")
			default:
			}
		})
	}
}

// The GUI usage sink regression cancels immediately after Complete while the
// inner provider has terminal usage ready. Also cover an open input and a
// saturated output: neither case may require producer EOF to keep accounting.
func TestMeteredCancellationPreservesReadyTerminalUsage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		closeInput   bool
		blockForward bool
	}{
		{name: "closed_input_before_receive", closeInput: true},
		{name: "open_input_before_receive"},
		{name: "open_input_blocked_forward", blockForward: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := Usage{Input: 123, Output: 45, Total: 168, CachedInput: 100, Reasoning: 20}
			var deltas []Delta
			if !tc.closeInput {
				for i := 0; i < meteredDeltaBuffer+1; i++ {
					deltas = append(deltas, Delta{Content: "queued content"})
				}
			}
			deltas = append(deltas, Delta{Usage: &usage})
			wrapperStats, contextStats := &sinkCapture{}, &sinkCapture{}
			ctx, cancel := context.WithCancel(WithCallSink(context.Background(), contextStats.sink()))
			ready, release, producerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			buffer := len(deltas)
			if tc.blockForward {
				buffer = 1
			}
			in := make(chan Delta, buffer)
			t.Cleanup(func() {
				cancel()
				close(release)
				waitMeterCancelSignal(t, producerDone, "ready-usage producer cleanup")
			})
			if tc.blockForward {
				go func() {
					defer close(producerDone)
					defer close(in)
					for _, d := range deltas {
						select {
						case in <- d:
						case <-ctx.Done():
							<-release
							return
						}
					}
					// With capacity one, ready usage means the wrapper already
					// consumed content #33 and is blocked behind its 32-slot out.
					close(ready)
					<-release
				}()
			} else {
				for _, d := range deltas {
					in <- d
				}
				close(ready)
				if tc.closeInput {
					close(in)
					close(producerDone)
				} else {
					go func() {
						<-release
						close(in)
						close(producerDone)
					}()
				}
			}
			recorded := make(chan struct{})
			capture := wrapperStats.sink()
			provider := Metered(immediateErrorProvider{complete: func(context.Context) (<-chan Delta, error) {
				if !tc.blockForward {
					// Cancellation precedes wrapper goroutine creation, so ready
					// content belongs to accounting only and must not be forwarded.
					cancel()
				}
				return in, nil
			}}, "test", PurposeMain, func(stat CallStat) {
				capture(stat)
				close(recorded)
			})
			stream, err := provider.Complete(ctx, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			waitMeterCancelSignal(t, ready, "terminal usage ready")
			cancel()
			// Leave output unread until the sink fires: the blocked-forward
			// case must finish via cancellation, never by a consumer freeing out.
			waitMeterCancelSignal(t, recorded, "terminal usage recorded before producer EOF")
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			forwarded := 0
		drainOutput:
			for {
				select {
				case d, ok := <-stream:
					if !ok {
						break drainOutput
					}
					if d.Usage != nil || d.Content != "queued content" {
						t.Fatalf("cancellation drain forwarded a delta: %+v", d)
					}
					forwarded++
				case <-deadline.C:
					t.Fatal("wrapper remained open after recording cancellation")
				}
			}
			wantForwarded := 0
			if tc.blockForward {
				wantForwarded = meteredDeltaBuffer
			}
			if forwarded != wantForwarded {
				t.Fatalf("forwarded %d content deltas; want %d accepted before cancellation", forwarded, wantForwarded)
			}
			wrapped, outer := wrapperStats.all(), contextStats.all()
			if len(wrapped) != 1 || len(outer) != 1 || !reflect.DeepEqual(wrapped, outer) {
				t.Fatalf("cancellation sinks differ: wrapper=%+v context=%+v", wrapped, outer)
			}
			stat := wrapped[0]
			if !stat.Canceled || stat.Failed || stat.TokensIn != usage.Input || stat.TokensOut != usage.Output ||
				stat.TokensCached != usage.CachedInput || stat.TokensReasoning != usage.Reasoning {
				t.Fatalf("ready terminal usage lost: %+v; want %+v", stat, usage)
			}
			if !tc.closeInput {
				select {
				case <-producerDone:
					t.Fatal("producer EOF hid missing cancellation accounting")
				default:
				}
			}
		})
	}
}
