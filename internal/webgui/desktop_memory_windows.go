package webgui

import (
	"errors"
	"log"

	"github.com/crgimenes/glaze"
)

// Only minimize/restore transitions change the target. Normal window resizing
// performs no COM work, and ongoing scripts, SSE and agent tasks remain active.
type nativeMemoryPolicy struct {
	set      func(bool) error
	low      bool
	disabled bool
}

func newNativeMemoryPolicy(view glaze.WebView) *nativeMemoryPolicy {
	p := &nativeMemoryPolicy{}
	if setter, ok := view.(interface{ SetMemoryUsageTarget(bool) error }); ok {
		p.set = setter.SetMemoryUsageTarget
	}
	return p
}

func (p *nativeMemoryPolicy) onSize(size uintptr) {
	if p == nil || p.disabled || p.set == nil {
		return
	}
	var low bool
	switch size {
	case 1: // SIZE_MINIMIZED
		low = true
	case 0, 2: // SIZE_RESTORED / SIZE_MAXIMIZED
	default:
		return
	}
	if low == p.low {
		return
	}
	if err := p.set(low); err != nil {
		if errors.Is(err, glaze.ErrMemoryUsageTargetUnavailable) {
			p.disabled = true
		}
		log.Printf("native WebView2 memory target unavailable: %v", err)
		return
	}
	p.low = low
}
