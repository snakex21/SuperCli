package glaze

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// ErrMemoryUsageTargetUnavailable identifies runtimes predating ICoreWebView2_19.
var ErrMemoryUsageTargetUnavailable = errors.New("webview2: memory usage target unavailable")

var iidMemoryUsageTarget = guid{0x6921F954, 0x79B0, 0x437F, [8]byte{0xA9, 0x97, 0xC8, 0x58, 0x11, 0x89, 0x7C, 0x68}}

// Microsoft WebView2 SDK 1.0.1901.177, ICoreWebView2_19. IUnknown occupies
// slots 0..2, inherited WebView methods 3..118, getter 119 and setter 120.
type memoryUsageTargetVtbl struct {
	iUnknownVtbl
	inherited [116]uintptr
	getLevel  uintptr
	putLevel  uintptr
}

type memoryUsageTarget struct{ vtbl *memoryUsageTargetVtbl }

func (w *webview) queryMemoryUsageTarget() (*memoryUsageTarget, error) {
	if w == nil || w.webview2 == 0 {
		return nil, errors.New("webview2: view is not initialized")
	}
	view := asWebView2(w.webview2)
	var out uintptr
	hr, _, _ := purego.SyscallN(view.vtbl.QueryInterface,
		w.webview2, uintptr(unsafe.Pointer(&iidMemoryUsageTarget)), uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(w)
	runtime.KeepAlive(&out)
	if uint32(hr) == 0x80004002 { // E_NOINTERFACE
		return nil, ErrMemoryUsageTargetUnavailable
	}
	if int32(hr) < 0 {
		return nil, fmt.Errorf("webview2: query memory target: HRESULT 0x%08x", uint32(hr))
	}
	if out == 0 {
		return nil, errors.New("webview2: query memory target returned an empty interface")
	}
	return (*memoryUsageTarget)(ptr(out)), nil
}

// SetMemoryUsageTarget requests best-effort memory reduction for an inactive
// view without suspending scripts or connections. Pass false when active again.
// Like other native methods, this must be called on the WebView's UI thread.
// An unsupported runtime leaves the view operating normally.
func (w *webview) SetMemoryUsageTarget(low bool) error {
	target, err := w.queryMemoryUsageTarget()
	if err != nil {
		return err
	}
	defer func() {
		purego.SyscallN(target.vtbl.Release, uintptr(unsafe.Pointer(target)))
		runtime.KeepAlive(target)
	}()
	level := uintptr(0) // COREWEBVIEW2_MEMORY_USAGE_TARGET_LEVEL_NORMAL
	if low {
		level = 1 // COREWEBVIEW2_MEMORY_USAGE_TARGET_LEVEL_LOW
	}
	hr, _, _ := purego.SyscallN(target.vtbl.putLevel, uintptr(unsafe.Pointer(target)), level)
	runtime.KeepAlive(target)
	if int32(hr) < 0 {
		return fmt.Errorf("webview2: set memory target: HRESULT 0x%08x", uint32(hr))
	}
	return nil
}
