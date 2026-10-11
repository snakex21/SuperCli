package glaze

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestMemoryTargetSDKLayout(t *testing.T) {
	var v memoryUsageTargetVtbl
	slot := unsafe.Sizeof(uintptr(0))
	if unsafe.Offsetof(v.getLevel)/slot != 119 || unsafe.Offsetof(v.putLevel)/slot != 120 || unsafe.Sizeof(v)/slot != 121 {
		t.Fatal("ICoreWebView2_19 layout differs from Microsoft WebView2.h")
	}
}

func TestMemoryTargetCOMLifetimeAndLevels(t *testing.T) {
	for _, failSetter := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "setter-error"}[failSetter], func(t *testing.T) {
			var levels []uintptr
			queries, releases := 0, 0
			wrongIID := false
			target := memoryUsageTarget{vtbl: &memoryUsageTargetVtbl{}}
			target.vtbl.Release = syscall.NewCallback(func(_ uintptr) uintptr { releases++; return 1 })
			target.vtbl.putLevel = syscall.NewCallback(func(_ uintptr, level uintptr) uintptr {
				levels = append(levels, level)
				if failSetter {
					return 0x80004005 // E_FAIL
				}
				return 0
			})
			base := iCoreWebView2{vtbl: &iCoreWebView2Vtbl{}}
			base.vtbl.QueryInterface = syscall.NewCallback(func(_ uintptr, iid, out uintptr) uintptr {
				queries++
				wrongIID = wrongIID || !guidEqual((*guid)(ptr(iid)), &iidMemoryUsageTarget)
				*(*uintptr)(ptr(out)) = uintptr(unsafe.Pointer(&target))
				return 0
			})
			w := &webview{webview2: uintptr(unsafe.Pointer(&base))}
			for _, low := range []bool{true, false} {
				err := w.SetMemoryUsageTarget(low)
				if (err != nil) != failSetter {
					t.Fatalf("low=%v error=%v", low, err)
				}
			}
			runtime.KeepAlive(&base)
			runtime.KeepAlive(&target)
			if wrongIID || queries != 2 || releases != 2 || len(levels) != 2 || levels[0] != 1 || levels[1] != 0 {
				t.Fatalf("wrongIID=%v queries=%d releases=%d levels=%v", wrongIID, queries, releases, levels)
			}
		})
	}
}

func TestMemoryTargetUnsupportedRuntime(t *testing.T) {
	base := iCoreWebView2{vtbl: &iCoreWebView2Vtbl{
		iUnknownVtbl: iUnknownVtbl{QueryInterface: syscall.NewCallback(func(_, _, _ uintptr) uintptr { return 0x80004002 })},
	}}
	w := &webview{webview2: uintptr(unsafe.Pointer(&base))}
	if err := w.SetMemoryUsageTarget(true); !errors.Is(err, ErrMemoryUsageTargetUnavailable) {
		t.Fatalf("error=%v", err)
	}
	runtime.KeepAlive(&base)
	if err := (&webview{}).SetMemoryUsageTarget(true); err == nil {
		t.Fatal("uninitialized view accepted")
	}
}

// Explicit integration test: creates a real runtime using only a temporary,
// portable profile. Each target is read back through the actual COM getter.
func TestMemoryTargetRealWebView2(t *testing.T) {
	if os.Getenv("SUPERCLI_TEST_WEBVIEW2") != "1" {
		t.Skip("set SUPERCLI_TEST_WEBVIEW2=1 for the native runtime integration test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t.Setenv("APPDATA", t.TempDir())
	view, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Destroy()
	w := view.(*webview)
	for _, low := range []bool{true, false, true, false} {
		if err := w.SetMemoryUsageTarget(low); err != nil {
			t.Fatal(err)
		}
		target, err := w.queryMemoryUsageTarget()
		if err != nil {
			t.Fatal(err)
		}
		var got int32
		hr, _, _ := purego.SyscallN(target.vtbl.getLevel, uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(&got)))
		purego.SyscallN(target.vtbl.Release, uintptr(unsafe.Pointer(target)))
		runtime.KeepAlive(target)
		runtime.KeepAlive(&got)
		want := int32(0)
		if low {
			want = 1
		}
		if int32(hr) < 0 || got != want {
			t.Fatalf("low=%v HRESULT=0x%08x level=%d want=%d", low, uint32(hr), got, want)
		}
	}
}
