//go:build windows

package windowautomation

import (
	"crypto/sha256"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var oleaut32 = windows.NewLazySystemDLL("oleaut32.dll")

// Vtable offsets follow the Windows SDK UIAutomationClient.h interfaces.
// No SetFocus, SendInput, mouse_event, clipboard, or foreground API is used.
type comObject struct{ table *[96]uintptr }

//go:uintptrescapes
func (o *comObject) call(index int, args ...uintptr) error {
	values := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	hr, _, _ := syscall.SyscallN(o.table[index], values...)
	if int32(hr) < 0 {
		return fmt.Errorf("UI Automation HRESULT 0x%08X", uint32(hr))
	}
	return nil
}
func (o *comObject) release() {
	if o != nil {
		_ = o.call(2)
	}
}

//go:uintptrescapes
func (o *comObject) child(index int, args ...uintptr) (*comObject, error) {
	var result *comObject
	// The out-pointer is appended to a variadic slice before the syscall.
	// Pin it explicitly: retaining only a uintptr cannot track a stack move.
	var pin runtime.Pinner
	pin.Pin(&result)
	defer pin.Unpin()
	args = append(args, uintptr(unsafe.Pointer(&result)))
	if err := o.call(index, args...); err != nil {
		return nil, err
	}
	return result, nil
}
func (o *comObject) integer(index int) (int32, error) {
	var result int32
	err := o.call(index, uintptr(unsafe.Pointer(&result)))
	return result, err
}
func (o *comObject) text(index int) string {
	text, _ := o.boundedText(index)
	return text
}
func (o *comObject) boundedText(index int) (string, bool) {
	text, truncated, _ := o.readBoundedText(index)
	return text, truncated
}
func (o *comObject) readBoundedText(index int) (string, bool, error) {
	var text *uint16
	if err := o.call(index, uintptr(unsafe.Pointer(&text))); err != nil {
		return "", false, err
	}
	if text == nil {
		return "", false, nil
	}
	defer oleaut32.NewProc("SysFreeString").Call(uintptr(unsafe.Pointer(text)))
	n, _, _ := oleaut32.NewProc("SysStringLen").Call(uintptr(unsafe.Pointer(text)))
	return windows.UTF16ToString(unsafe.Slice(text, int(min(n, 1024)))), n > 1024, nil
}

type controlPattern struct {
	id     uintptr
	guid   *ole.GUID
	method string
}

var invokePattern = controlPattern{10000, ole.NewGUID("{FB377FBE-8EA6-46D5-9C73-6499642D3059}"), "InvokePattern.Invoke"}
var togglePattern = controlPattern{10015, ole.NewGUID("{94CF8058-9B8D-4AB9-8BFD-4CD0A33C8C70}"), "TogglePattern.Toggle"}
var selectPattern = controlPattern{10010, ole.NewGUID("{A8EFA66A-0FDA-421A-9194-38021F3578EA}"), "SelectionItemPattern.Select"}
var valuePattern = controlPattern{10002, ole.NewGUID("{A94CD8B1-0844-4CD6-9D2D-640537AB39E9}"), "ValuePattern.SetValue"}

// Menu-bar items and submenu entries often expose only ExpandCollapse; Expand
// shares vtable slot 3 with Invoke/Toggle/Select.
var expandPattern = controlPattern{10005, ole.NewGUID("{619BE086-1F4E-4EE4-BAFA-210128738730}"), "ExpandCollapsePattern.Expand"}

// A parent menu/tree item may expose both Invoke/Select and ExpandCollapse.
// Expand its children rather than selecting/invoking the parent. Leaf nodes
// still use Invoke/Toggle/Select, regardless of the application's language.
var clickPatterns = []controlPattern{expandPattern, invokePattern, togglePattern, selectPattern}

func expansionState(state int32) string {
	switch state {
	case 0:
		return "collapsed"
	case 1:
		return "expanded"
	case 2:
		return "partially_expanded"
	case 3:
		return "leaf"
	}
	return ""
}

func (o *comObject) pattern(p controlPattern) *comObject {
	// GetCurrentPatternAs requests the actual pattern interface rather than
	// assuming the returned IUnknown pointer has a pattern's vtable.
	result, err := o.child(14, p.id, uintptr(unsafe.Pointer(p.guid)))
	if err != nil {
		return nil
	}
	return result
}

func runtimeID(o *comObject) string {
	var array uintptr
	if o.call(4, uintptr(unsafe.Pointer(&array))) != nil || array == 0 {
		return ""
	}
	defer oleaut32.NewProc("SafeArrayDestroy").Call(array)
	var low, high int32
	hr, _, _ := oleaut32.NewProc("SafeArrayGetLBound").Call(array, 1, uintptr(unsafe.Pointer(&low)))
	if int32(hr) < 0 {
		return ""
	}
	hr, _, _ = oleaut32.NewProc("SafeArrayGetUBound").Call(array, 1, uintptr(unsafe.Pointer(&high)))
	if int32(hr) < 0 || high < low || int64(high)-int64(low) >= 64 {
		return ""
	}
	var data *int32
	hr, _, _ = oleaut32.NewProc("SafeArrayAccessData").Call(array, uintptr(unsafe.Pointer(&data)))
	if int32(hr) < 0 || data == nil {
		return ""
	}
	defer oleaut32.NewProc("SafeArrayUnaccessData").Call(array)
	return fmt.Sprint(unsafe.Slice(data, int(high-low+1)))
}

func describe(o *comObject, r Request, hwnd uintptr, depth int, actions bool) (Element, error) {
	pid, err := o.integer(20)
	if err != nil || int(pid) != r.PID {
		return Element{}, fmt.Errorf("element is outside the selected process")
	}
	password, err := o.integer(35)
	if err != nil {
		return Element{}, err
	} // no speculative reads of secret values
	enabled, _ := o.integer(28)
	e := Element{Depth: depth, Role: o.text(22), Enabled: enabled != 0, Password: password != 0}
	if e.Password {
		return e, nil
	}
	e.Name, e.AutomationID = o.text(23), o.text(29)
	// Mutation requests need the same fresh identity/value checks, but do not
	// return this tree. Avoid probing every control's click patterns again.
	if actions {
		for _, p := range clickPatterns {
			if pattern := o.pattern(p); pattern != nil {
				if p.id == expandPattern.id {
					state, err := pattern.integer(5) // CurrentExpandCollapseState
					e.Expansion = expansionState(state)
					if err != nil || state == 3 {
						pattern.release()
						continue
					}
				}
				pattern.release()
				if e.Enabled {
					e.Actions = append(e.Actions, "click")
				}
				break
			}
		}
	}
	if pattern := o.pattern(valuePattern); pattern != nil {
		var readErr error
		e.Value, e.ValueTruncated, readErr = pattern.readBoundedText(4)
		if readErr != nil {
			pattern.release()
			return Element{}, readErr // do not hash/overwrite an unobserved value
		}
		if actions {
			readOnly, readErr := pattern.integer(5)
			if readErr == nil && readOnly == 0 && e.Enabled && !e.ValueTruncated {
				e.Actions = append(e.Actions, "type")
			}
		}
		pattern.release()
	}
	if id := runtimeID(o); id != "" {
		// Include the observed semantics/value. A stale element ID must not
		// silently refer to a replacement control or overwrite a newly edited value.
		hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%X:%s:%s:%s:%s:%s", r.PID, r.Created, hwnd, id, e.Role, e.Name, e.AutomationID, e.Value)))
		e.ID = fmt.Sprintf("el_%x", hash[:16])
	}
	if e.ID == "" {
		e.Actions = nil
	}
	return e, nil
}

func windowPID(hwnd uintptr) uint32 {
	var pid uint32
	user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func windowCaption(hwnd uintptr) string {
	text := make([]uint16, 513)
	user32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	return windows.UTF16ToString(text)
}

func windowClass(hwnd uintptr) string {
	class := make([]uint16, 256)
	user32.NewProc("GetClassNameW").Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	return windows.UTF16ToString(class)
}

const popupMenuClass = "#32768"

func windowCandidate(hwnd uintptr) WindowCandidate {
	visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd)
	enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(hwnd)
	owner, _, _ := user32.NewProc("GetWindow").Call(hwnd, 4) // GW_OWNER
	styleProc := "GetWindowLongPtrW"
	if unsafe.Sizeof(uintptr(0)) == 4 {
		styleProc = "GetWindowLongW"
	}
	exStyle, _, _ := user32.NewProc(styleProc).Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE (-20)
	class := windowClass(hwnd)
	return WindowCandidate{
		HWND: hwnd, Title: windowCaption(hwnd), Visible: visible != 0, Enabled: enabled != 0, Owner: owner,
		Menu:      class == popupMenuClass,
		Auxiliary: exStyle&0x80 != 0 || class == "IME" || class == "MSCTFIME UI", // WS_EX_TOOLWINDOW
	}
}

// openPopupMenus lists visible classic popup menus created by the thread that
// owns hwnd. They are separate top-level windows, absent from hwnd's subtree.
func openPopupMenus(hwnd uintptr, pid int) []uintptr {
	thread, _, _ := user32.NewProc("GetWindowThreadProcessId").Call(hwnd, 0)
	if thread == 0 {
		return nil
	}
	var menus []uintptr
	callback := windows.NewCallback(func(menu, _ uintptr) uintptr {
		var owner uint32
		menuThread, _, _ := user32.NewProc("GetWindowThreadProcessId").Call(menu, uintptr(unsafe.Pointer(&owner)))
		if owner != uint32(pid) || menuThread != thread || windowClass(menu) != popupMenuClass {
			return 1
		}
		if visible, _, _ := user32.NewProc("IsWindowVisible").Call(menu); visible == 0 {
			return 1
		}
		menus = append(menus, menu)
		if len(menus) >= 8 {
			return 0
		}
		return 1
	})
	user32.NewProc("EnumWindows").Call(callback, 0)
	return menus
}

func selectedWindow(r Request) (uintptr, error) {
	if r.WindowID != "" {
		base, text := 10, r.WindowID
		if strings.HasPrefix(strings.ToLower(text), "0x") {
			base, text = 16, text[2:]
		}
		n, err := strconv.ParseUint(text, base, strconv.IntSize)
		if err != nil || n == 0 {
			return 0, fmt.Errorf("invalid window_id")
		}
		hwnd := uintptr(n)
		root, _, _ := user32.NewProc("GetAncestor").Call(hwnd, 2)
		if windowPID(hwnd) != uint32(r.PID) || root != hwnd {
			return 0, fmt.Errorf("window_id is not a top-level window of this process")
		}
		return hwnd, nil
	}
	var exact, partial []uintptr
	callback := windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		if windowPID(hwnd) != uint32(r.PID) {
			return 1
		}
		visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd)
		if visible == 0 {
			return 1
		}
		title := windowCaption(hwnd)
		if r.WindowTitle != "" && strings.EqualFold(title, r.WindowTitle) {
			exact = append(exact, hwnd)
		}
		if r.WindowTitle == "" || strings.Contains(strings.ToLower(title), strings.ToLower(r.WindowTitle)) {
			partial = append(partial, hwnd)
		}
		return 1
	})
	user32.NewProc("EnumWindows").Call(callback, 0)
	if len(exact) > 0 {
		partial = exact
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	candidates := make([]WindowCandidate, 0, len(partial))
	for _, hwnd := range partial {
		candidates = append(candidates, windowCandidate(hwnd))
	}
	if r.WindowTitle == "" {
		// Without a selector, skip popup menus and prefer the window that can
		// take input (e.g. a modal dialog over its disabled owner).
		if i := PickWindow(candidates); i >= 0 {
			return candidates[i].HWND, nil
		}
	}
	if len(partial) == 0 {
		return 0, fmt.Errorf("process has 0 matching windows; provide an unambiguous window_title or window_id")
	}
	return 0, ambiguousWindowError(candidates)
}

func runUIAutomation(r Request, process *Process) (Result, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		return Result{}, err
	}
	defer ole.CoUninitialize()
	unknown, err := ole.CreateInstance(ole.NewGUID("{FF48DBA4-60EF-4201-AA87-54103EEF594E}"), ole.NewGUID("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}"))
	if err != nil {
		return Result{}, err
	}
	automation := (*comObject)(unsafe.Pointer(unknown))
	defer automation.release()
	hwnd, err := selectedWindow(r)
	if err != nil {
		return Result{}, err
	}
	root, err := automation.child(6, hwnd) // ElementFromHandle
	if err != nil || root == nil {
		return Result{}, fmt.Errorf("window accessibility unavailable: %v", err)
	}
	defer root.release()
	walker, err := automation.child(14) // ControlViewWalker
	if err != nil || walker == nil {
		return Result{}, fmt.Errorf("accessibility tree unavailable: %v", err)
	}
	defer walker.release()
	result := Result{PID: r.PID, WindowID: fmt.Sprintf("0x%X", hwnd), Title: windowCaption(hwnd)}
	menus := openPopupMenus(hwnd, r.PID)
	limit := MaxElements
	if len(menus) > 0 {
		limit = MaxElements / 2 // leave room for the open menu's items
	}
	var target *comObject
	var targetHWND uintptr
	defer func() { target.release() }()
	current, menuTag := hwnd, ""
	var visit func(*comObject, int) error
	visit = func(node *comObject, depth int) error {
		if len(result.Elements) >= limit {
			result.Truncated = true
			return nil
		}
		e, err := describe(node, r, current, depth, r.Action == "inspect_window")
		if err != nil {
			return nil
		} // never traverse an out-of-process subtree
		e.PopupMenu = menuTag
		result.Elements = append(result.Elements, e)
		if e.Password {
			return nil
		}
		if e.ID != "" && e.ID == r.ElementID {
			if target != nil {
				return fmt.Errorf("ambiguous element identity")
			}
			_ = node.call(1)
			target, targetHWND = node, current
		}
		if depth >= 12 {
			result.Truncated = true
			return nil
		}
		child, err := walker.child(4, uintptr(unsafe.Pointer(node)))
		if err != nil {
			return err
		}
		for child != nil {
			if err := visit(child, depth+1); err != nil {
				child.release()
				return err
			}
			if len(result.Elements) >= limit {
				child.release()
				result.Truncated = true
				break
			}
			next, err := walker.child(6, uintptr(unsafe.Pointer(child)))
			child.release()
			if err != nil {
				return err
			}
			child = next
		}
		return nil
	}
	if err := visit(root, 0); err != nil {
		return Result{}, err
	}
	limit = MaxElements
	for _, menu := range menus {
		if len(result.Elements) >= limit {
			result.Truncated = true
			break
		}
		menuRoot, err := automation.child(6, menu) // ElementFromHandle
		if err != nil || menuRoot == nil {
			continue
		}
		// Menu elements hash their own hwnd, so click IDs resolve to this menu.
		current, menuTag = menu, fmt.Sprintf("0x%X", menu)
		err = visit(menuRoot, 0)
		menuRoot.release()
		if err != nil {
			return Result{}, err
		}
	}
	if len(result.Elements) == 0 {
		return Result{}, fmt.Errorf("window exposes no accessible elements in this process")
	}
	if r.Action == "inspect_window" {
		return result, nil
	}
	if target == nil {
		return Result{}, fmt.Errorf("element_id is stale, unavailable or outside the bounded tree; inspect_window again")
	}
	if err := process.Alive(); err != nil {
		return Result{}, err
	}
	if windowPID(targetHWND) != uint32(r.PID) {
		return Result{}, fmt.Errorf("window ownership changed")
	}
	fresh, err := describe(target, r, targetHWND, 0, false)
	if err != nil || fresh.ID != r.ElementID || fresh.Password || !fresh.Enabled {
		return Result{}, fmt.Errorf("element changed, is disabled or is password protected; inspect_window again")
	}
	if r.Action == "type" && fresh.ValueTruncated {
		return Result{}, fmt.Errorf("field value exceeds the observation limit; refusing to overwrite partially observed text")
	}
	patterns := clickPatterns
	if r.Action == "type" {
		patterns = []controlPattern{valuePattern}
	}
	for _, p := range patterns {
		pattern := target.pattern(p)
		if pattern == nil {
			continue
		}
		if p.id == expandPattern.id {
			state, err := pattern.integer(5)
			if err != nil || state == 3 { // LeafNode has no children to expand.
				pattern.release()
				continue
			}
			if state == 1 {
				pattern.release()
				result.Elements = nil
				observed := true
				result.EffectObserved, result.Observation = &observed, "already_expanded"
				return result, nil
			}
		}
		if r.Action == "type" {
			readonly, readErr := pattern.integer(5)
			if readErr != nil || readonly != 0 {
				pattern.release()
				return Result{}, fmt.Errorf("element is read-only")
			}
			text, err := windows.UTF16PtrFromString(r.Text)
			if err != nil {
				pattern.release()
				return Result{}, err
			}
			err = pattern.call(3, uintptr(unsafe.Pointer(text)))
			runtime.KeepAlive(text)
			if err != nil {
				pattern.release()
				return Result{}, err
			}
		} else if err := pattern.call(3); err != nil {
			pattern.release()
			return Result{}, err
		}
		// One readback saves a separate inspect for the common type/expand
		// case. It never waits/polls or repeats a potentially mutating action.
		if r.Action == "type" {
			value, truncated, readErr := pattern.readBoundedText(4)
			observed := readErr == nil && !truncated && value == r.Text
			result.EffectObserved, result.Observation = &observed, "value_matches"
			if !observed {
				result.Observation = "value_not_confirmed; inspect before retrying"
			}
		} else if p.id == expandPattern.id {
			state, err := pattern.integer(5)
			observed := err == nil && state == 1
			result.EffectObserved, result.Observation = &observed, "expanded"
			if !observed {
				result.Observation = "expansion_not_confirmed; do not repeat the click blindly"
				if minimized, _, _ := user32.NewProc("IsIconic").Call(hwnd); minimized != 0 {
					result.Observation += "; this provider may require a visible window"
				}
			}
		}
		pattern.release()
		result.Elements = nil
		result.Dispatched, result.Method = true, "UIAutomation."+p.method
		return result, nil
	}
	return Result{}, fmt.Errorf("element does not expose a background %s pattern; no focus or physical input fallback", r.Action)
}
