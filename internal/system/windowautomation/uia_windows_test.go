//go:build windows

package windowautomation

import (
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

// Count actual pattern requests on a synthetic COM object. A mutation scan
// still reads the value used in the stale-ID check, but never probes click
// patterns for every unrelated control. The object returns no secret values.
func TestMutationScanDoesNotProbeUnrelatedClickPatterns(t *testing.T) {
	var requested []uintptr
	table := [96]uintptr{}
	integer := windows.NewCallback(func(_ uintptr, out *int32) uintptr {
		*out = 0
		return 0
	})
	table[20] = windows.NewCallback(func(_ uintptr, out *int32) uintptr {
		*out = 123
		return 0
	})
	table[28], table[35] = integer, integer
	empty := windows.NewCallback(func(_ uintptr, out *uintptr) uintptr {
		*out = 0
		return 0
	})
	table[4], table[22], table[23], table[29] = empty, empty, empty, empty
	table[14] = windows.NewCallback(func(_ uintptr, id uintptr, _ uintptr, out *uintptr) uintptr {
		requested = append(requested, id)
		*out = 0
		return 0
	})
	object := &comObject{table: &table}
	request := Request{Identity: Identity{PID: 123}}
	for _, actions := range []bool{true, false} {
		requested = nil
		if _, err := describe(object, request, 1, 0, actions); err != nil {
			t.Fatal(err)
		}
		if actions {
			if len(requested) != 5 {
				t.Fatalf("inspection pattern requests=%v", requested)
			}
		} else if len(requested) != 1 || requested[0] != valuePattern.id {
			t.Fatalf("mutation scanned unrelated patterns: %v", requested)
		}
	}
	runtime.KeepAlive(object)
}

func TestExpandableParentsPrecedeInvokeAndLeafNodesAreExplicit(t *testing.T) {
	if clickPatterns[0].id != expandPattern.id || expansionState(3) != "leaf" || expansionState(1) != "expanded" || expansionState(-1) != "" {
		t.Fatal("expandable parent/leaf handling regressed")
	}
}

func TestFailedValueReadIsNotAnObservedEmptyString(t *testing.T) {
	table := [96]uintptr{}
	table[4] = windows.NewCallback(func(_ uintptr, _ uintptr) uintptr { return 0x80004005 }) // E_FAIL
	object := &comObject{table: &table}
	if value, _, err := object.readBoundedText(4); err == nil {
		t.Fatalf("failed read was treated as a verified empty value: %q", value)
	}
	runtime.KeepAlive(object)
}

func TestUnknownExpansionStateDoesNotAdvertiseOrTryAnotherClick(t *testing.T) {
	patternTable := [96]uintptr{}
	patternTable[2] = windows.NewCallback(func(_ uintptr) uintptr { return 0 })
	patternTable[5] = windows.NewCallback(func(_ uintptr, _ *int32) uintptr { return 0x80004005 })
	pattern := &comObject{table: &patternTable}
	table := [96]uintptr{}
	table[20] = windows.NewCallback(func(_ uintptr, out *int32) uintptr { *out = 123; return 0 })
	table[35] = windows.NewCallback(func(_ uintptr, out *int32) uintptr { *out = 0; return 0 })
	table[28] = windows.NewCallback(func(_ uintptr, out *int32) uintptr { *out = 1; return 0 })
	empty := windows.NewCallback(func(_ uintptr, out *uintptr) uintptr { *out = 0; return 0 })
	table[4], table[22], table[23], table[29] = empty, empty, empty, empty
	var requests []uintptr
	table[14] = windows.NewCallback(func(_ uintptr, id uintptr, _ uintptr, out **comObject) uintptr {
		requests = append(requests, id)
		*out = nil
		if id == expandPattern.id {
			*out = pattern
		}
		return 0
	})
	object := &comObject{table: &table}
	element, err := describe(object, Request{Identity: Identity{PID: 123}}, 1, 0, true)
	if err != nil || element.Expansion != "" || len(requests) != 2 || requests[0] != expandPattern.id || requests[1] != valuePattern.id {
		t.Fatalf("unknown expansion state used a speculative click: element=%+v requests=%v err=%v", element, requests, err)
	}
	runtime.KeepAlive(object)
	runtime.KeepAlive(pattern)
}
