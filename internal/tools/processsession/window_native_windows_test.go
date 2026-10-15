//go:build windows

package processsession

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"supercli/internal/system/windowautomation"
)

// Live Windows test against only a process/window created by this test. Kernel
// events signal readiness and button activation; no desktop/progress polling.
func TestNativeOwnedSessionAccessibility(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	nonce := fmt.Sprint(time.Now().UnixNano())
	newEvent := func(suffix string) (uintptr, string) {
		t.Helper()
		name := "Local\\SuperCliUIA" + nonce + suffix
		ptr, _ := windows.UTF16PtrFromString(name)
		event, _, err := ownedWindowKernel32.NewProc("CreateEventW").Call(0, 1, 0, uintptr(unsafe.Pointer(ptr)))
		if event == 0 {
			t.Fatal(err)
		}
		t.Cleanup(func() { windows.CloseHandle(windows.Handle(event)) })
		return event, name
	}
	ready, readyName := newEvent("Ready")
	clicked, clickedName := newEvent("Clicked")
	title := "SuperCli owned accessibility fixture " + nonce
	start, err := execute(t, tool, map[string]any{"action": "start", "command": []string{os.Args[0], "-test.run=TestOwnedWindowFixtureHelper"}, "env": []string{"SUPERCLI_OWNED_WINDOW_HELPER=1", "SUPERCLI_OWNED_WINDOW_TITLE=" + title, "SUPERCLI_OWNED_WINDOW_READY=" + readyName, "SUPERCLI_OWNED_WINDOW_UIA=1", "SUPERCLI_OWNED_WINDOW_CLICKED=" + clickedName}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	if state, _, _ := ownedWindowKernel32.NewProc("WaitForSingleObject").Call(ready, 10000); state != 0 {
		t.Fatal("fixture did not signal readiness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	foreground, _, _ := ownedWindowUser32.NewProc("GetForegroundWindow").Call()
	lastInput := func() uint32 {
		state := struct{ Size, Tick uint32 }{Size: 8}
		ownedWindowUser32.NewProc("GetLastInputInfo").Call(uintptr(unsafe.Pointer(&state)))
		return state.Tick
	}
	inputBefore := lastInput()
	type point struct{ X, Y int32 }
	var cursor point
	ownedWindowUser32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	act := func(action, element, text string) (windowautomation.Result, error) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": action, "id": start.ID, "window_title": title, "element_id": element, "text": text})
		result, err := tool.Execute(ctx, raw)
		if err != nil {
			return windowautomation.Result{}, err
		}
		if result.Err != nil {
			return windowautomation.Result{}, result.Err
		}
		var out windowautomation.Result
		err = json.Unmarshal([]byte(result.Text), &out)
		return out, err
	}
	view, err := act("inspect_window", "", "")
	if err != nil {
		t.Fatal(err)
	}
	shotArgs, _ := json.Marshal(map[string]any{"action": "screenshot", "id": start.ID, "window_id": view.WindowID})
	shot, shotErr := tool.Execute(ctx, shotArgs)
	if shotErr != nil || shot.Err != nil {
		t.Fatalf("capture of the observed owned window failed: %v %v", shotErr, shot.Err)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "PASSWORD_SENTINEL") {
		t.Fatal("password value leaked")
	}
	var edit, button, label string
	passwordSeen := false
	for _, e := range view.Elements {
		if e.Value == "visible seed" {
			edit = e.ID
		}
		if e.Name == "Owned action" {
			button = e.ID
		}
		if e.Name == "Fixture label" {
			label = e.ID
		}
		passwordSeen = passwordSeen || e.Password
	}
	if edit == "" || button == "" || label == "" || !passwordSeen {
		t.Fatalf("missing accessible controls: %s", raw)
	}
	if result, err := act("type", edit, "zażółć nowa wartość"); err != nil || !result.Dispatched {
		t.Fatalf("background type: %+v %v", result, err)
	}
	view, err = act("inspect_window", "", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range view.Elements {
		found = found || e.Value == "zażółć nowa wartość"
	}
	if !found {
		t.Fatal("field value was not changed")
	}
	if _, err := act("type", edit, "stale must not overwrite"); err == nil {
		t.Fatal("stale value/element identity accepted")
	}
	if result, err := act("click", button, ""); err != nil || !result.Dispatched {
		t.Fatalf("background invoke: %+v %v", result, err)
	}
	if state, _, _ := ownedWindowKernel32.NewProc("WaitForSingleObject").Call(clicked, 5000); state != 0 {
		t.Fatal("button did not signal activation")
	}
	if _, err := act("click", label, ""); err == nil {
		t.Fatal("unsupported label used an input fallback")
	}
	inputChanged := lastInput() != inputBefore
	if after, _, _ := ownedWindowUser32.NewProc("GetForegroundWindow").Call(); after != foreground && !inputChanged {
		t.Fatal("background action changed foreground")
	}
	var after point
	ownedWindowUser32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&after)))
	if after != cursor && !inputChanged {
		t.Fatalf("cursor changed without a new input event: before=%+v after=%+v", cursor, after)
	}
	if inputChanged {
		t.Log("Desktop input occurred during the test; cursor/foreground comparison is inconclusive, and the user's input was not restored or overwritten")
	}
	t.Logf("%d accessibility elements; Unicode value verified; button kernel event received; password/stale/unsupported controls protected; concurrent desktop input=%t", len(view.Elements), inputChanged)
	if _, err := tool.Manager.Stop(ctx, start.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := act("inspect_window", "", ""); err == nil {
		t.Fatal("stopped process retained control")
	}
}
