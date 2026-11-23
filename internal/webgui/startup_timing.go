package webgui

import (
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/agent"
)

// Start-up timing for "how fast does the interface appear". With --debug
// (agent.TurnTimingOn) the log gets one line per start-up stage and per first
// request of each path in the first minute, all measured from process start:
//
//	startup listening +95ms
//	startup GET /api/sessions +412ms (12ms)
var (
	processStart atomic.Int64 // unix nanoseconds; 0 = unknown
	startupSeen  sync.Map     // path -> struct{}
)

// MarkProcessStart records when the process started (call first in main).
func MarkProcessStart(t time.Time) { processStart.Store(t.UnixNano()) }

func sinceProcessStart() time.Duration {
	start := processStart.Load()
	if start == 0 {
		return 0
	}
	return time.Since(time.Unix(0, start))
}

// StartupTimingf logs one start-up stage when timing is enabled.
func StartupTimingf(stage string) {
	if !agent.TurnTimingOn() || processStart.Load() == 0 {
		return
	}
	log.Printf("startup %s +%s", stage, sinceProcessStart().Round(time.Millisecond))
}

// withStartupTiming logs the first request of every path made in the first
// minute after start, with its own duration.
func withStartupTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !agent.TurnTimingOn() || processStart.Load() == 0 || sinceProcessStart() > time.Minute {
			next.ServeHTTP(w, r)
			return
		}
		if _, seen := startupSeen.LoadOrStore(r.URL.Path, struct{}{}); seen {
			next.ServeHTTP(w, r)
			return
		}
		began := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("startup %s %s +%s (%s)", r.Method, r.URL.Path, sinceProcessStart().Round(time.Millisecond), time.Since(began).Round(time.Millisecond))
	})
}
