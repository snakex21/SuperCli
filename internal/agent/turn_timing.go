package agent

import (
	"log"
	"os"
	"sync/atomic"
	"time"
)

// Turn-end timing for debugging "the model finished but nothing happens for
// a few seconds". Off by default; on with --debug (supercli-web) or
// SUPERCLI_TURN_TIMING=1. Each line names a step after the model's last
// token and how long it took.
var turnTiming atomic.Bool

func init() {
	if os.Getenv("SUPERCLI_TURN_TIMING") != "" {
		turnTiming.Store(true)
	}
}

// SetTurnTiming turns the turn-end timing log on or off.
func SetTurnTiming(on bool) { turnTiming.Store(on) }

// TurnTimingOn reports whether turn-end timing is logged.
func TurnTimingOn() bool { return turnTiming.Load() }

// TurnTimingf logs one timing step when enabled: "turn-end <step> +12ms".
func TurnTimingf(step string, since time.Time) {
	if !turnTiming.Load() || since.IsZero() {
		return
	}
	log.Printf("turn-end %s +%s", step, time.Since(since).Round(time.Millisecond))
}
