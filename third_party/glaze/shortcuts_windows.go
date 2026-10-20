package glaze

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Verified against Microsoft.Web.WebView2 1.0.2903.40 build/native/include/WebView2.h:
// args2 inherits the six args methods after IUnknown; its getter/setter are slots 9/10.
// https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2acceleratorkeypressedeventargs2
// Unlike put_Handled(TRUE), put_IsBrowserAcceleratorKeyEnabled(FALSE) keeps DOM
// delivery intact, including app bindings which share a browser accelerator.
var (
	iidAcceleratorArgs2   = guid{0x03b2c8c8, 0x7799, 0x4e34, [8]byte{0xbd, 0x66, 0xed, 0x26, 0xaa, 0x85, 0xf2, 0xbf}}
	iidAcceleratorHandler = guid{0xb29c7e28, 0xfa79, 0x41a8, [8]byte{0x8e, 0x44, 0x65, 0x81, 0x1c, 0x76, 0xdc, 0xb2}}
	shortcutKeyState      = syscall.NewLazyDLL("user32.dll").NewProc("GetKeyState")
	shortcutVtblOnce      sync.Once
	shortcutVtbl          *comHandlerVtbl
	shortcutHandlersMu    sync.Mutex
	shortcutHandlers      = map[*shortcutHandler]struct{}{}
)

type acceleratorArgsVtbl struct {
	iUnknownVtbl
	getKind, getKey, getLParam, getPhysicalStatus, getHandled, putHandled uintptr
}
type acceleratorArgs2Vtbl struct {
	acceleratorArgsVtbl
	getBrowserEnabled, putBrowserEnabled uintptr
}
type acceleratorArgs2 struct{ vtbl *acceleratorArgs2Vtbl }

// Deliberately narrow: no blanket accelerator setting, context-menu change, or
// OS hook. Alt (including AltGr), Windows keys and ordinary editing are untouched.
func protectedBrowserShortcut(key uint32, ctrl, shift, alt, meta bool) bool {
	if alt || meta {
		return false
	}
	if key == 0x74 {
		return true
	} // F5, Shift+F5, Ctrl+F5
	if key == 0x7b {
		return !ctrl && !shift
	} // F12
	if !ctrl {
		return false
	}
	if key == 'R' {
		return true
	}
	if shift {
		return key == 'I' || key == 'J' || key == 'C'
	}
	return key == 'P' || key == 'S' || key == 'U'
}

type shortcutHandler struct {
	vtbl     *comHandlerVtbl
	refs     int32
	active   int32
	keyState func(int) bool
}

func newShortcutHandler() *shortcutHandler {
	shortcutVtblOnce.Do(func() {
		shortcutVtbl = &comHandlerVtbl{
			QueryInterface: syscall.NewCallback(shortcutQueryInterface), AddRef: syscall.NewCallback(shortcutAddRef),
			Release: syscall.NewCallback(shortcutRelease), Invoke: syscall.NewCallback(shortcutInvoke),
		}
	})
	h := &shortcutHandler{vtbl: shortcutVtbl, refs: 1, active: 1, keyState: func(key int) bool { v, _, _ := shortcutKeyState.Call(uintptr(key)); return int16(v) < 0 }}
	shortcutHandlersMu.Lock()
	shortcutHandlers[h] = struct{}{}
	shortcutHandlersMu.Unlock()
	return h
}
func shortcutQueryInterface(this, iid, out uintptr) uintptr {
	if out == 0 || iid == 0 {
		return 0x80004003
	}
	*(*uintptr)(ptr(out)) = 0
	want := (*guid)(ptr(iid))
	if !guidEqual(want, &iidIUnknown) && !guidEqual(want, &iidAcceleratorHandler) {
		return 0x80004002
	}
	*(*uintptr)(ptr(out)) = this
	shortcutAddRef(this)
	return 0
}
func shortcutAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&(*shortcutHandler)(ptr(this)).refs, 1))
}
func shortcutRelease(this uintptr) uintptr {
	h := (*shortcutHandler)(ptr(this))
	n := atomic.AddInt32(&h.refs, -1)
	if n == 0 {
		shortcutHandlersMu.Lock()
		delete(shortcutHandlers, h)
		shortcutHandlersMu.Unlock()
	}
	return uintptr(n)
}
func (h *shortcutHandler) releaseOwner() {
	atomic.StoreInt32(&h.active, 0)
	shortcutRelease(uintptr(unsafe.Pointer(h)))
}
func shortcutInvoke(this, _, args uintptr) uintptr {
	h := (*shortcutHandler)(ptr(this))
	if atomic.LoadInt32(&h.active) == 0 || args == 0 {
		return 0
	}
	a := (*acceleratorArgs2)(ptr(args)) // only the base slots are read before QI
	var kind, key uint32
	hr, _, _ := purego.SyscallN(a.vtbl.getKind, args, uintptr(unsafe.Pointer(&kind)))
	if int32(hr) < 0 || (kind != 0 && kind != 2) {
		return 0
	} // KEY_DOWN / SYSTEM_KEY_DOWN
	hr, _, _ = purego.SyscallN(a.vtbl.getKey, args, uintptr(unsafe.Pointer(&key)))
	if int32(hr) < 0 || !protectedBrowserShortcut(key, h.keyState(0x11), h.keyState(0x10), h.keyState(0x12), h.keyState(0x5b) || h.keyState(0x5c)) {
		return 0
	}
	var out uintptr
	hr, _, _ = purego.SyscallN(a.vtbl.QueryInterface, args, uintptr(unsafe.Pointer(&iidAcceleratorArgs2)), uintptr(unsafe.Pointer(&out)))
	if int32(hr) >= 0 && out != 0 {
		extended := (*acceleratorArgs2)(ptr(out))
		// Downlevel runtimes (or setter failures) retain DOM fallback only. Never
		// substitute Handled=true: that would swallow the application's own bindings.
		purego.SyscallN(extended.vtbl.putBrowserEnabled, out, 0)
		purego.SyscallN(extended.vtbl.Release, out)
		runtime.KeepAlive(extended)
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(h)
	runtime.KeepAlive(&kind)
	runtime.KeepAlive(&key)
	runtime.KeepAlive(&out)
	return 0
}

// EnableSelectiveBrowserShortcuts is an optional, Windows-only extension. Call
// on the UI thread before navigation. Ordinary glaze users remain unchanged.
// Requires args2 support in the runtime for native suppression (SDK 1.0.2210.55+);
// callers should also install a document-start DOM fallback. Debug/manual DevTools
// access is not disabled. The subscription is removed by Destroy.
func (w *webview) EnableSelectiveBrowserShortcuts() error {
	if w == nil || w.controller == 0 {
		return errors.New("webview2: controller is not initialized")
	}
	if w.shortcutFilter != nil {
		return nil
	}
	h := newShortcutHandler()
	ctrl := asController(w.controller)
	var token uint64
	hr, _, _ := purego.SyscallN(ctrl.vtbl.AddAcceleratorKeyPressed, w.controller, uintptr(unsafe.Pointer(h)), uintptr(unsafe.Pointer(&token)))
	runtime.KeepAlive(h)
	runtime.KeepAlive(&token)
	if int32(hr) < 0 {
		h.releaseOwner()
		return fmt.Errorf("webview2: subscribe selective shortcuts: HRESULT 0x%08x", uint32(hr))
	}
	w.shortcutFilter = h
	w.shortcutToken = token
	return nil
}
func (w *webview) detachShortcutFilter() {
	if w.shortcutFilter == nil {
		return
	}
	h := w.shortcutFilter
	atomic.StoreInt32(&h.active, 0)
	if w.controller != 0 {
		ctrl := asController(w.controller)
		purego.SyscallN(ctrl.vtbl.RemoveAcceleratorKeyPressed, w.controller, uintptr(w.shortcutToken))
	}
	w.shortcutFilter = nil
	w.shortcutToken = 0
	h.releaseOwner()
}
