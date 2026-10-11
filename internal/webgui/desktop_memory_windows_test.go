package webgui

import (
	"errors"
	"reflect"
	"testing"

	"github.com/crgimenes/glaze"
)

func TestNativeMemoryMinimizeRestoreTransitions(t *testing.T) {
	var calls []bool
	p := &nativeMemoryPolicy{set: func(low bool) error { calls = append(calls, low); return nil }}
	for _, size := range []uintptr{0, 2, 0, 3, 1, 1, 3, 2, 2, 0, 1, 0} {
		p.onSize(size)
	}
	if !reflect.DeepEqual(calls, []bool{true, false, true, false}) || p.low {
		t.Fatalf("calls=%v low=%v", calls, p.low)
	}
}

func TestNativeMemoryUnsupportedRuntimeDisablesOnlyOptimization(t *testing.T) {
	calls := 0
	p := &nativeMemoryPolicy{set: func(bool) error { calls++; return glaze.ErrMemoryUsageTargetUnavailable }}
	for _, size := range []uintptr{1, 0, 1, 2, 1} {
		p.onSize(size)
	}
	if calls != 1 || !p.disabled || p.low {
		t.Fatalf("calls=%d disabled=%v low=%v", calls, p.disabled, p.low)
	}
	newNativeMemoryPolicy(nil).onSize(1)
}

func TestNativeMemoryFailedTransitionRetriesOnLaterWindowEvent(t *testing.T) {
	var calls []bool
	fail := true
	p := &nativeMemoryPolicy{set: func(low bool) error {
		calls = append(calls, low)
		if fail {
			return errors.New("temporary COM failure")
		}
		return nil
	}}
	p.onSize(1)
	if p.low || p.disabled {
		t.Fatal("failed minimize was recorded as successful")
	}
	fail = false
	p.onSize(1)
	fail = true
	p.onSize(0)
	if !p.low || p.disabled {
		t.Fatal("failed restore was recorded as successful")
	}
	fail = false
	p.onSize(2)
	if !reflect.DeepEqual(calls, []bool{true, true, false, false}) || p.low {
		t.Fatalf("calls=%v low=%v", calls, p.low)
	}
}
