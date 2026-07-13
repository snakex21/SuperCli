//go:build windows

package webgui

import (
	"fmt"
	"log"
	"unsafe"

	"github.com/crgimenes/glaze"
	"golang.org/x/sys/windows"
)

var (
	user32DLL         = windows.NewLazySystemDLL("user32.dll")
	kernel32DLL       = windows.NewLazySystemDLL("kernel32.dll")
	shell32DLL        = windows.NewLazySystemDLL("shell32.dll")
	shcoreDLL         = windows.NewLazySystemDLL("shcore.dll")
	loadImageW        = user32DLL.NewProc("LoadImageW")
	sendMessageW      = user32DLL.NewProc("SendMessageW")
	getSystemMetricsW = user32DLL.NewProc("GetSystemMetrics")
	getModuleW        = kernel32DLL.NewProc("GetModuleHandleW")
	setAppID          = shell32DLL.NewProc("SetCurrentProcessExplicitAppUserModelID")
	setDPIContext     = user32DLL.NewProc("SetProcessDpiAwarenessContext")
	setDPIAware       = user32DLL.NewProc("SetProcessDPIAware")
	setDPIShcore      = shcoreDLL.NewProc("SetProcessDpiAwareness")
)

const (
	imageIcon     = 1
	lrDefaultSize = 0x0040
	lrShared      = 0x8000
	wmSetIcon     = 0x0080
	iconSmall     = 0
	iconBig       = 1
	appIconID     = 1
	smCXScreen    = 0
	smCYScreen    = 1
)

// runNativeAppWindow hosts the local GUI in a real Win32 window backed by the
// system WebView2 runtime. The process therefore has SuperCli's own taskbar and
// Alt+Tab identity instead of appearing as a Chrome/Edge app-mode window.
func runNativeAppWindow(url string) error {
	enablePerMonitorDPI()
	setNativeAppIdentity()
	w, err := glaze.New(false)
	if err != nil {
		return fmt.Errorf("native WebView2 unavailable: %w", err)
	}
	defer w.Destroy()

	w.SetTitle("SuperCli")
	badge, badgeErr := newNativeTaskbarBadge(uintptr(w.Window()))
	if badgeErr != nil {
		log.Printf("native taskbar badge unavailable: %v", badgeErr)
	}
	if badge != nil {
		defer badge.Close()
		defer registerNativeCompletion(badge.Completed)()
		log.Printf("native taskbar badge ready")
	}
	// WebView2 does not consistently expose the web Badging API. Use the native
	// taskbar overlay when available and retain the title counter only as a
	// fallback for Windows editions/configurations that suppress overlays.
	_ = w.Bind("supercliSetBadge", func(count int) {
		if badge != nil {
			badge.Set(count)
			w.SetTitle("SuperCli")
			return
		}
		if count > 0 {
			w.SetTitle(fmt.Sprintf("(%d) SuperCli", count))
			return
		}
		w.SetTitle("SuperCli")
	})
	setNativeWindowIcon(w.Window())
	width, height := nativeWindowSize()
	w.SetSize(width, height, glaze.HintNone)
	// The web UI switches to an overlay drawer well before this minimum, so a
	// compact laptop screen keeps a useful conversation area instead of a
	// permanently docked inspector consuming half of the client width.
	w.SetSize(720, 520, glaze.HintMin)
	w.Navigate(url)
	w.Run()
	return nil
}

func nativeWindowSize() (int, int) {
	width, height := 1440, 900
	screenWidth, _, _ := getSystemMetricsW.Call(smCXScreen)
	screenHeight, _, _ := getSystemMetricsW.Call(smCYScreen)
	if screenWidth > 0 {
		if available := int(screenWidth) - 64; available < width {
			width = available
		}
	}
	if screenHeight > 0 {
		if available := int(screenHeight) - 96; available < height {
			height = available
		}
	}
	if width < 720 {
		width = 720
	}
	if height < 520 {
		height = 520
	}
	return width, height
}

func enablePerMonitorDPI() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 is the signed pseudo-handle
	// -4. It makes WebView2 rasterize at the monitor's native scale instead of
	// letting Windows bitmap-stretch a lower-resolution window (visible blur at
	// 125%/150%). Fall back for older Windows builds.
	perMonitorV2 := ^uintptr(3)
	if setDPIContext.Find() == nil {
		if ok, _, _ := setDPIContext.Call(perMonitorV2); ok != 0 {
			return
		}
	}
	if setDPIShcore.Find() == nil {
		const processPerMonitorDPIAware = 2
		if hr, _, _ := setDPIShcore.Call(processPerMonitorDPIAware); int32(hr) >= 0 {
			return
		}
	}
	if setDPIAware.Find() == nil {
		_, _, _ = setDPIAware.Call()
	}
}

func setNativeAppIdentity() {
	id, err := windows.UTF16PtrFromString("SuperCli.Desktop")
	if err == nil {
		_, _, _ = setAppID.Call(uintptr(unsafe.Pointer(id)))
	}
}

func setNativeWindowIcon(window unsafe.Pointer) {
	instance, _, _ := getModuleW.Call(0)
	icon, _, _ := loadImageW.Call(instance, appIconID, imageIcon, 0, 0, lrDefaultSize|lrShared)
	if icon == 0 {
		return
	}
	_, _, _ = sendMessageW.Call(uintptr(window), wmSetIcon, iconBig, icon)
	_, _, _ = sendMessageW.Call(uintptr(window), wmSetIcon, iconSmall, icon)
}
