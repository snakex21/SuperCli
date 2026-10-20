package glaze

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestSelectiveShortcutPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		key                          uint32
		ctrl, shift, alt, meta, want bool
	}{
		{"reload", 'R', true, false, false, false, true}, {"hard reload", 'R', true, true, false, false, true},
		{"F5", 0x74, false, false, false, false, true}, {"CtrlF5", 0x74, true, false, false, false, true},
		{"print", 'P', true, false, false, false, true}, {"save", 'S', true, false, false, false, true}, {"source", 'U', true, false, false, false, true},
		{"F12", 0x7b, false, false, false, false, true}, {"inspector", 'I', true, true, false, false, true}, {"console", 'J', true, true, false, false, true}, {"picker", 'C', true, true, false, false, true},
		{"copy", 'C', true, false, false, false, false}, {"paste", 'V', true, false, false, false, false}, {"cut", 'X', true, false, false, false, false},
		{"undo", 'Z', true, false, false, false, false}, {"redo", 'Z', true, true, false, false, false}, {"redo Y", 'Y', true, false, false, false, false}, {"all", 'A', true, false, false, false, false},
		{"zoom+", 0xbb, true, false, false, false, false}, {"zoom-", 0xbd, true, false, false, false, false}, {"zoom0", '0', true, false, false, false, false},
		{"close", 'W', true, false, false, false, false}, {"AltF4", 0x73, false, false, true, false, false},
		{"AltGr", 'R', true, false, true, false, false}, {"AltR", 'R', false, false, true, false, false}, {"text", 'R', false, false, false, false, false},
		{"IME process", 0xe5, true, false, false, false, false}, {"WinR", 'R', false, false, false, true, false}, {"shift save", 'S', true, true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := protectedBrowserShortcut(tc.key, tc.ctrl, tc.shift, tc.alt, tc.meta); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSelectiveShortcutSDKLayout(t *testing.T) {
	var v acceleratorArgs2Vtbl
	slot := unsafe.Sizeof(uintptr(0))
	if unsafe.Offsetof(v.getBrowserEnabled)/slot != 9 || unsafe.Offsetof(v.putBrowserEnabled)/slot != 10 || unsafe.Sizeof(v)/slot != 11 {
		t.Fatal("args2 layout differs from SDK")
	}
}

func TestSelectiveShortcutCOM(t *testing.T) {
	for _, mode := range []string{"supported", "downlevel", "setter-failure"} {
		t.Run(mode, func(t *testing.T) {
			queries, releases, sets, handled := 0, 0, 0, 0
			a := acceleratorArgs2{vtbl: &acceleratorArgs2Vtbl{}}
			var pin runtime.Pinner
			pin.Pin(&a)
			defer pin.Unpin()
			a.vtbl.getKind = syscall.NewCallback(func(_, out uintptr) uintptr { *(*uint32)(ptr(out)) = 0; return 0 })
			a.vtbl.getKey = syscall.NewCallback(func(_, out uintptr) uintptr { *(*uint32)(ptr(out)) = 'R'; return 0 })
			a.vtbl.putHandled = syscall.NewCallback(func(_, v uintptr) uintptr { handled++; return 0 })
			a.vtbl.QueryInterface = syscall.NewCallback(func(this, iid, out uintptr) uintptr {
				queries++
				if !guidEqual((*guid)(ptr(iid)), &iidAcceleratorArgs2) {
					t.Error("wrong args2 IID")
				}
				if mode == "downlevel" {
					return 0x80004002
				}
				*(*uintptr)(ptr(out)) = this
				return 0
			})
			a.vtbl.Release = syscall.NewCallback(func(_ uintptr) uintptr { releases++; return 1 })
			a.vtbl.putBrowserEnabled = syscall.NewCallback(func(_, v uintptr) uintptr {
				sets++
				if v != 0 {
					t.Error("browser accelerator enabled")
				}
				if mode == "setter-failure" {
					return 0x80004005
				}
				return 0
			})
			filter := newShortcutHandler()
			filter.keyState = func(k int) bool { return k == 0x11 }
			purego.SyscallN(filter.vtbl.Invoke, uintptr(unsafe.Pointer(filter)), 0, uintptr(unsafe.Pointer(&a)))
			filter.releaseOwner()
			want := 1
			if mode == "downlevel" {
				want = 0
			}
			if queries != 1 || sets != want || releases != want || handled != 0 {
				t.Fatalf("queries=%d sets=%d releases=%d handled=%d", queries, sets, releases, handled)
			}
			runtime.KeepAlive(&a)
		})
	}
}

func TestSelectiveShortcutHandlerInterfaces(t *testing.T) {
	h := newShortcutHandler()
	this := uintptr(unsafe.Pointer(h))
	var out uintptr
	for _, iid := range []*guid{&iidIUnknown, &iidAcceleratorHandler} {
		hr, _, _ := purego.SyscallN(h.vtbl.QueryInterface, this, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
		if hr != 0 || out != this || h.refs != 2 {
			t.Fatalf("QI hr=%x refs=%d", hr, h.refs)
		}
		purego.SyscallN(h.vtbl.Release, out)
	}
	other := guid{Data1: 123}
	out = 999
	hr, _, _ := purego.SyscallN(h.vtbl.QueryInterface, this, uintptr(unsafe.Pointer(&other)), uintptr(unsafe.Pointer(&out)))
	if uint32(hr) != 0x80004002 || out != 0 || h.refs != 1 {
		t.Fatal("unknown interface accepted")
	}
	hr, _, _ = purego.SyscallN(h.vtbl.QueryInterface, this, uintptr(unsafe.Pointer(&iidIUnknown)), 0)
	if uint32(hr) != 0x80004003 {
		t.Fatal("null output accepted")
	}
	h.releaseOwner()
	runtime.KeepAlive(h)
	runtime.KeepAlive(&out)
	runtime.KeepAlive(&other)
}

func TestSelectiveShortcutIgnoresOrdinaryKeysAndKeyUp(t *testing.T) {
	for _, tc := range []struct {
		key, kind uint32
		ctrl, alt bool
	}{{'R', 1, true, false}, {'R', 3, true, false}, {'R', 0, true, true}, {'C', 0, true, false}, {0xe5, 0, true, false}} {
		h := newShortcutHandler()
		h.keyState = func(k int) bool { return (k == 0x11 && tc.ctrl) || (k == 0x12 && tc.alt) }
		a := acceleratorArgs2{vtbl: &acceleratorArgs2Vtbl{}}
		var pin runtime.Pinner
		pin.Pin(&a)
		defer pin.Unpin()
		a.vtbl.getKind = syscall.NewCallback(func(_, out uintptr) uintptr { *(*uint32)(ptr(out)) = tc.kind; return 0 })
		a.vtbl.getKey = syscall.NewCallback(func(_, out uintptr) uintptr { *(*uint32)(ptr(out)) = tc.key; return 0 })
		a.vtbl.QueryInterface = syscall.NewCallback(func(_, _, _ uintptr) uintptr { t.Error("ordinary key queried for suppression"); return 0x80004002 })
		purego.SyscallN(h.vtbl.Invoke, uintptr(unsafe.Pointer(h)), 0, uintptr(unsafe.Pointer(&a)))
		h.releaseOwner()
		runtime.KeepAlive(&a)
	}
}

func TestSelectiveShortcutSubscriptionLifetime(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "registration-failure"}[fail], func(t *testing.T) {
			var h *shortcutHandler
			adds, removes := 0, 0
			ctrl := iController{vtbl: &iCoreWebView2ControllerVtbl{}}
			var pin runtime.Pinner
			pin.Pin(&ctrl)
			defer pin.Unpin()
			ctrl.vtbl.AddAcceleratorKeyPressed = syscall.NewCallback(func(_, handler, token uintptr) uintptr {
				adds++
				h = (*shortcutHandler)(ptr(handler))
				if fail {
					return 0x80004005
				}
				purego.SyscallN(h.vtbl.AddRef, handler)
				*(*uint64)(ptr(token)) = 73
				return 0
			})
			ctrl.vtbl.RemoveAcceleratorKeyPressed = syscall.NewCallback(func(_, token uintptr) uintptr {
				removes++
				if token != 73 {
					t.Error("wrong token")
				}
				purego.SyscallN(h.vtbl.Release, uintptr(unsafe.Pointer(h)))
				return 0
			})
			w := &webview{controller: uintptr(unsafe.Pointer(&ctrl))}
			if err := w.EnableSelectiveBrowserShortcuts(); (err != nil) != fail {
				t.Fatalf("error=%v", err)
			}
			if !fail {
				if err := w.EnableSelectiveBrowserShortcuts(); err != nil {
					t.Fatal(err)
				}
			}
			ctrl.vtbl.Close = syscall.NewCallback(func(_ uintptr) uintptr {
				if !fail && (removes != 1 || h.active != 0) {
					t.Error("controller closed before disabling/removing shortcut callback")
				}
				return 0
			})
			ctrl.vtbl.Release = syscall.NewCallback(func(_ uintptr) uintptr { return 0 })
			w.Destroy()
			w.Destroy()
			w.detachShortcutFilter()
			wantRemove := 1
			if fail {
				wantRemove = 0
			}
			if adds != 1 || removes != wantRemove || h.refs != 0 {
				t.Fatalf("adds=%d removes=%d refs=%d", adds, removes, h.refs)
			}
			shortcutHandlersMu.Lock()
			_, kept := shortcutHandlers[h]
			shortcutHandlersMu.Unlock()
			if kept {
				t.Fatal("callback leaked")
			}
			runtime.KeepAlive(&ctrl)
		})
	}
}
