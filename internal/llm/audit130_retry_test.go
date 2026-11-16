package llm

// Audit-130: retry settings (max_retries 0/N/-1, retry_unknown_errors),
// Retry-After and backoff timing on the real clock, requests_per_minute
// measured on the mock, and cancellation / goroutine hygiene — on both the
// Chat Completions and the Responses wire.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func a130Status(status int, retryAfter string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"message":"mock status %d","type":"mock"}}`, status)
	}
}

func a130OK(wire a130Wire, w http.ResponseWriter) {
	a130TextStream(wire, w, "ok")
	a130Finish(wire, w)
}

func a130Gaps(ts []time.Time) []time.Duration {
	var out []time.Duration
	for i := 1; i < len(ts); i++ {
		out = append(out, ts[i].Sub(ts[i-1]))
	}
	return out
}

// 2a. 429 burst WITH Retry-After: the header is honoured on the real clock.
func TestA130_429BurstWithRetryAfter(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, 5, false)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n <= 2 {
					a130Status(http.StatusTooManyRequests, "1")(w)
					return
				}
				a130OK(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 20*time.Second)
			gaps := a130Gaps(m.Arrivals())
			t.Logf("%s: err=%v requests=%d gaps=%v notices=%q elapsed=%s", wire, res.Err, m.requests.Load(), gaps, res.Notices, res.Elapsed)
			if res.Err != nil || res.Content != "ok" || m.requests.Load() != 3 || len(res.Notices) != 2 {
				t.Fatalf("429 burst: %+v requests=%d", res, m.requests.Load())
			}
			for _, g := range gaps {
				if g < 950*time.Millisecond || g > 1500*time.Millisecond {
					t.Fatalf("Retry-After: 1 not honoured: gaps=%v", gaps)
				}
			}
			if !strings.Contains(res.Notices[0], "rate limited (HTTP 429)") || !strings.Contains(res.Notices[1], "retry 2/5") {
				t.Fatalf("notices = %q", res.Notices)
			}
		})
	}
}

// 2b. 503 burst WITHOUT Retry-After: exponential backoff 0.5 s base with
// jitter in [nominal/2, nominal] (docs: 0.5 s, 1 s, 2 s, ...).
func TestA130_503BurstBackoffTiming(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, 5, false)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n <= 3 {
					a130Status(http.StatusServiceUnavailable, "")(w)
					return
				}
				a130OK(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 20*time.Second)
			gaps := a130Gaps(m.Arrivals())
			t.Logf("%s: err=%v requests=%d gaps=%v elapsed=%s", wire, res.Err, m.requests.Load(), gaps, res.Elapsed)
			if res.Err != nil || m.requests.Load() != 4 {
				t.Fatalf("503 burst: err=%v requests=%d", res.Err, m.requests.Load())
			}
			for i, g := range gaps {
				nominal := 500 * time.Millisecond << i
				if g < nominal/2-20*time.Millisecond || g > nominal+250*time.Millisecond {
					t.Fatalf("gap %d = %s outside [%s, %s]", i+1, g, nominal/2, nominal)
				}
			}
		})
	}
}

// 2c. max_retries = 0 / N: exact request counts, terminal error surfaced.
func TestA130_MaxRetriesExactCounts(t *testing.T) {
	for _, wire := range a130Wires {
		for _, n := range []int{0, 1, 3} {
			t.Run(fmt.Sprintf("%s/max=%d", wire, n), func(t *testing.T) {
				a130WithRetries(t, n, false)
				m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) {
					a130Status(http.StatusServiceUnavailable, "0")(w)
				})
				p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
				res := a130Run(t, context.Background(), p, 10*time.Second)
				if res.Err == nil || !strings.Contains(res.Err.Error(), "503") {
					t.Fatalf("want terminal 503, got %v", res.Err)
				}
				if got := int(m.requests.Load()); got != n+1 {
					t.Fatalf("requests = %d, want %d", got, n+1)
				}
				if len(res.Notices) != n {
					t.Fatalf("notices = %d, want %d", len(res.Notices), n)
				}
			})
		}
	}
}

// 2d. max_retries = -1 retries past the default budget until success.
func TestA130_UnlimitedRetriesUntilSuccess(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, UnlimitedRetries, false)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n <= 12 {
					a130Status(http.StatusBadGateway, "0")(w)
					return
				}
				a130OK(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 20*time.Second)
			if res.Err != nil || m.requests.Load() != 13 || len(res.Notices) != 12 || !strings.Contains(res.Notices[11], "12/∞") {
				t.Fatalf("unlimited: err=%v requests=%d notices=%q", res.Err, m.requests.Load(), res.Notices)
			}
		})
	}
}

// 2e. max_retries = -1 against a permanently failing endpoint stops promptly
// when the caller cancels DURING a real retry wait; no goroutine leak.
func TestA130_CancelDuringRetryWait(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, UnlimitedRetries, false)
			check := a130GoroutineBaseline(t)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				a130Status(http.StatusServiceUnavailable, "30")(w)
			})
			p, tr := a130Provider(t, wire, m.URL(), 5*time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			cancelAt := time.AfterFunc(400*time.Millisecond, cancel)
			defer cancelAt.Stop()
			start := time.Now()
			res := a130Run(t, ctx, p, 10*time.Second)
			lag := res.Elapsed - 400*time.Millisecond
			t.Logf("%s: err=%v requests=%d elapsed=%s cancel->close lag=%s", wire, res.Err, m.requests.Load(), time.Since(start), lag)
			if lag > 200*time.Millisecond || m.requests.Load() != 1 {
				t.Fatalf("cancel during 30 s retry wait: lag=%s requests=%d", lag, m.requests.Load())
			}
			m.srv.Close()
			check(2, tr.CloseIdleConnections)
		})
	}
}

// 2f. Cancellation mid-stream: the channel closes promptly, the server sees
// the connection go away, and nothing leaks.
func TestA130_CancelMidStream(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			check := a130GoroutineBaseline(t)
			serverSawCancel := make(chan time.Duration, 1)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				a130SSEHeaders(w)
				start := time.Now()
				for {
					line := chatContentLine("tok ")
					if wire == wireResponses {
						line = respTextLine("tok ")
					}
					if !a130Write(w, line) {
						break
					}
					select {
					case <-r.Context().Done():
						serverSawCancel <- time.Since(start)
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
				serverSawCancel <- time.Since(start)
			})
			p, tr := a130Provider(t, wire, m.URL(), 5*time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(300*time.Millisecond, cancel)
			res := a130Run(t, ctx, p, 10*time.Second)
			lag := res.Elapsed - 300*time.Millisecond
			var serverLag time.Duration
			select {
			case serverLag = <-serverSawCancel:
			case <-time.After(3 * time.Second):
				t.Fatal("server never saw the client go away")
			}
			t.Logf("%s: err=%v content=%dB clientLag=%s serverSawCloseAfter=%s", wire, res.Err, len(res.Content), lag, serverLag)
			if lag > 200*time.Millisecond {
				t.Fatalf("mid-stream cancel lag %s", lag)
			}
			m.srv.Close()
			check(2, tr.CloseIdleConnections)
		})
	}
}

// 2g. retry_unknown_errors: an unclassified status (418) is retried only when
// enabled; a permanent one (401) never.
func TestA130_RetryUnknownErrors(t *testing.T) {
	for _, wire := range a130Wires {
		for _, tc := range []struct {
			status  int
			unknown bool
			want    int32
		}{
			{http.StatusTeapot, false, 1},
			{http.StatusTeapot, true, 3},
			{http.StatusUnauthorized, true, 1},
			{http.StatusBadRequest, true, 1},
		} {
			t.Run(fmt.Sprintf("%s/%d/unknown=%v", wire, tc.status, tc.unknown), func(t *testing.T) {
				a130WithRetries(t, 2, tc.unknown)
				m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) {
					a130Status(tc.status, "0")(w)
				})
				p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
				res := a130Run(t, context.Background(), p, 10*time.Second)
				if res.Err == nil || m.requests.Load() != tc.want {
					t.Fatalf("requests=%d want %d err=%v", m.requests.Load(), tc.want, res.Err)
				}
			})
		}
	}
}

// 2h. A 429 with Retry-After > 60 s is a quota window: surfaced at once, and
// the terminal error must keep the typed Retry-After so a FailoverProvider
// can keep that backend asleep (failover.go failureCooldown). Both wires
// return the typed HTTPResponseError.
func TestA130_LongRetryAfterQuotaWindowTyped(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, 5, false)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				a130Status(http.StatusTooManyRequests, "3600")(w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			if res.Err == nil || m.requests.Load() != 1 || res.Elapsed > time.Second {
				t.Fatalf("quota window: err=%v requests=%d elapsed=%s", res.Err, m.requests.Load(), res.Elapsed)
			}
			wait, is429 := RateLimitRetryAfter(res.Err)
			cooldown := (&FailoverProvider{}).failureCooldown(res.Err, 1)
			t.Logf("%s: err=%q typed429=%v retryAfter=%s failoverCooldown=%s", wire, res.Err, is429, wait, cooldown)
			if !is429 || wait != time.Hour || cooldown != time.Hour {
				t.Fatalf("typed 429 metadata lost: typed429=%v retryAfter=%s cooldown=%s", is429, wait, cooldown)
			}
		})
	}
}

// a130VClock is a virtual clock for the limiter: retrySleep registers a
// wake-up and blocks; the clock advances to the earliest wake-up only when
// every active worker is asleep, so no time passes between a limiter
// reservation and the request reaching the mock.
type a130VClock struct {
	mu       sync.Mutex
	now      time.Time
	active   int
	sleepers []*a130Sleeper
}

type a130Sleeper struct {
	wake time.Time
	ch   chan struct{}
}

func (c *a130VClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *a130VClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	s := &a130Sleeper{wake: c.now.Add(d), ch: make(chan struct{})}
	c.sleepers = append(c.sleepers, s)
	c.mu.Unlock()
	select {
	case <-s.ch:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *a130VClock) drive(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-time.After(time.Millisecond):
		}
		c.mu.Lock()
		if len(c.sleepers) > 0 && len(c.sleepers) >= c.active {
			earliest := c.sleepers[0].wake
			for _, s := range c.sleepers {
				if s.wake.Before(earliest) {
					earliest = s.wake
				}
			}
			if earliest.After(c.now) {
				c.now = earliest
			}
			keep := c.sleepers[:0]
			for _, s := range c.sleepers {
				if !s.wake.After(c.now) {
					close(s.ch)
				} else {
					keep = append(keep, s)
				}
			}
			c.sleepers = keep
		}
		c.mu.Unlock()
	}
}

// 2i. requests_per_minute on a virtual clock: at most N request starts in any
// 60 s window, measured from the mock's own arrival timestamps, with one and
// with three parallel workers sharing the endpoint budget.
func TestA130_RequestsPerMinuteVirtualClock(t *testing.T) {
	for _, wire := range a130Wires {
		for _, parallel := range []int{1, 3} {
			t.Run(fmt.Sprintf("%s/workers=%d", wire, parallel), func(t *testing.T) {
				vc := &a130VClock{now: time.Unix(1_000_000, 0), active: parallel}
				savedNow, savedSleep := limiterNow, retrySleep
				limiterNow, retrySleep = vc.Now, vc.Sleep
				stop := make(chan struct{})
				go vc.drive(stop)
				t.Cleanup(func() { close(stop); limiterNow, retrySleep = savedNow, savedSleep; ResetRequestLimits() })
				ResetRequestLimits()
				const rpm, total = 5, 12
				m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) { a130OK(wire, w) })
				m.clock = vc.Now
				SetRequestsPerMinute(m.URL(), rpm)
				p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
				var wg sync.WaitGroup
				var noticeMu sync.Mutex
				notices := 0
				for wkr := 0; wkr < parallel; wkr++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer func() { vc.mu.Lock(); vc.active--; vc.mu.Unlock() }()
						for i := 0; i < total/parallel; i++ {
							res := a130Run(t, context.Background(), p, 10*time.Second)
							if res.Err != nil {
								t.Errorf("request failed: %v", res.Err)
							}
							noticeMu.Lock()
							notices += len(res.Notices)
							noticeMu.Unlock()
						}
					}()
				}
				wg.Wait()
				arr := m.Arrivals()
				if len(arr) != total {
					t.Fatalf("arrivals=%d want %d", len(arr), total)
				}
				maxInWindow := 0
				for i := range arr {
					k := 0
					for j := range arr {
						if !arr[j].Before(arr[i]) && arr[j].Sub(arr[i]) < time.Minute {
							k++
						}
					}
					maxInWindow = max(maxInWindow, k)
				}
				first, last := arr[0], arr[0]
				for _, a := range arr {
					if a.Before(first) {
						first = a
					}
					if a.After(last) {
						last = a
					}
				}
				span := last.Sub(first)
				minSpan := time.Duration((total-1)/rpm) * time.Minute
				t.Logf("%s workers=%d: %d requests, max %d in any 60 s window (limit %d), virtual span %s (ideal %s), notices=%d", wire, parallel, total, maxInWindow, rpm, span, minSpan, notices)
				if maxInWindow > rpm {
					t.Fatalf("limiter allowed %d starts in one minute (limit %d)", maxInWindow, rpm)
				}
				if span < minSpan || span > minSpan+time.Second {
					t.Fatalf("virtual span %s, want %s (limiter idles or over-admits)", span, minSpan)
				}
				if notices == 0 {
					t.Fatal("no limiter notice was emitted")
				}
			})
		}
	}
}

// 2i'. Soak: the same on the REAL clock (rpm 6, 8 requests => >= 60 s).
func TestA130_SoakRequestsPerMinuteRealClock(t *testing.T) {
	a130Soak(t)
	ResetRequestLimits()
	t.Cleanup(ResetRequestLimits)
	m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) { a130OK(wireChat, w) })
	SetRequestsPerMinute(m.URL(), 6)
	p, _ := a130Provider(t, wireChat, m.URL(), 5*time.Second)
	start := time.Now()
	for i := 0; i < 8; i++ {
		if res := a130Run(t, context.Background(), p, 90*time.Second); res.Err != nil {
			t.Fatal(res.Err)
		}
	}
	arr := m.Arrivals()
	var offs []string
	for _, a := range arr {
		offs = append(offs, a.Sub(start).Round(100*time.Millisecond).String())
	}
	t.Logf("real-clock arrivals: %v", offs)
	if arr[6].Sub(arr[0]) < 59*time.Second {
		t.Fatalf("7th request started %s after the first, limit 6/min", arr[6].Sub(arr[0]))
	}
}

// 2j. Many requests through mixed faults leave no goroutines or idle
// connections behind (settle loop).
func TestA130_NoGoroutineLeakAcrossMixedFaults(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, 2, false)
			check := a130GoroutineBaseline(t)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				switch n % 5 {
				case 0:
					a130OK(wire, w)
				case 1:
					a130TextStream(wire, w, "partial")
					a130Abort()
				case 2:
					a130Status(http.StatusServiceUnavailable, "0")(w)
				case 3:
					a130TextStream(wire, w, "x")
					a130Write(w, "data: {bad json\n\n")
				case 4:
					a130Hijack(w)
				}
			})
			p, tr := a130Provider(t, wire, m.URL(), 2*time.Second)
			errs := 0
			for i := 0; i < 150; i++ {
				if res := a130Run(t, context.Background(), p, 10*time.Second); res.Err != nil {
					errs++
				}
			}
			m.srv.Close()
			extra := check(3, tr.CloseIdleConnections)
			t.Logf("%s: 150 calls, %d errors, %d requests, goroutines after settle: +%d", wire, errs, m.requests.Load(), extra)
		})
	}
}

// 2k. A connection dropped before any response (hijack + close) is
// delivered in-stream as an "http:" error. It is NOT a typed startup error;
// it is a typed StreamTransportError classified transient, so the agent's
// in-stream path retries it.
func TestA130_DroppedConnectionBeforeHeadersClassification(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			a130WithRetries(t, 5, false)
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, _ int) { a130Hijack(w) })
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
			if err != nil {
				t.Fatalf("Complete returned a startup error: %v", err)
			}
			var got error
			for d := range ch {
				if d.Err != nil && got == nil {
					got = d.Err
				}
			}
			t.Logf("%s: in-stream err=%q class=%s requests=%d", wire, got, ClassifyProviderError(got), m.requests.Load())
			if got == nil || errors.Is(got, context.Canceled) {
				t.Fatalf("want transport error, got %v", got)
			}
			var transport *StreamTransportError
			if !errors.As(got, &transport) || ClassifyProviderError(got) != RetryTransient || !strings.HasPrefix(got.Error(), "http: ") {
				t.Fatalf("dropped connection not a transient StreamTransportError: %T %v (%s)", got, got, ClassifyProviderError(got))
			}
		})
	}
}
