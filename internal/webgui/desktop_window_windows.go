//go:build windows

package webgui

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/crgimenes/glaze"
	"golang.org/x/sys/windows"
)

var (
	user32DLL          = windows.NewLazySystemDLL("user32.dll")
	kernel32DLL        = windows.NewLazySystemDLL("kernel32.dll")
	shell32DLL         = windows.NewLazySystemDLL("shell32.dll")
	shcoreDLL          = windows.NewLazySystemDLL("shcore.dll")
	loadImageW         = user32DLL.NewProc("LoadImageW")
	sendMessageW       = user32DLL.NewProc("SendMessageW")
	getSystemMetricsW  = user32DLL.NewProc("GetSystemMetrics")
	getWindowPlacement = user32DLL.NewProc("GetWindowPlacement")
	showWindowW        = user32DLL.NewProc("ShowWindow")
	setWindowLongW     = user32DLL.NewProc("SetWindowLongPtrW")
	setClassLongW      = user32DLL.NewProc("SetClassLongPtrW")
	callWindowProcW    = user32DLL.NewProc("CallWindowProcW")
	messageBoxW        = user32DLL.NewProc("MessageBoxW")
	getModuleW         = kernel32DLL.NewProc("GetModuleHandleW")
	setAppID           = shell32DLL.NewProc("SetCurrentProcessExplicitAppUserModelID")
	setDPIContext      = user32DLL.NewProc("SetProcessDpiAwarenessContext")
	setDPIAware        = user32DLL.NewProc("SetProcessDPIAware")
	setDPIShcore       = shcoreDLL.NewProc("SetProcessDpiAwareness")
)

const (
	imageIcon      = 1
	lrDefaultSize  = 0x0040
	lrLoadFromFile = 0x0010
	lrShared       = 0x8000
	wmSetIcon      = 0x0080
	iconSmall      = 0
	iconBig        = 1
	gclpHIcon      = -14
	gclpHIconSmall = -34
	appIconID      = 1
	smCXScreen     = 0
	smCYScreen     = 1
	wmClose        = 0x0010
	mbYesNo        = 0x00000004
	mbIconWarning  = 0x00000030
	mbDefButton2   = 0x00000100
	idYes          = 6
	swMaximize     = 3
)

type nativePoint struct {
	X int32
	Y int32
}

type nativeRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type nativeWindowPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCommand    uint32
	MinPosition    nativePoint
	MaxPosition    nativePoint
	NormalPosition nativeRect
}

type nativeSaveResult struct {
	Saved bool   `json:"saved"`
	Path  string `json:"path,omitempty"`
}

// runNativeAppWindow hosts the local GUI in a real Win32 window backed by the
// system WebView2 runtime. The process therefore has SuperCli's own taskbar and
// Alt+Tab identity instead of appearing as a Chrome/Edge app-mode window.
func runNativeAppWindow(url, appName, dataDir, iconPath string, hasActiveWork func() bool) error {
	enablePerMonitorDPI()
	setNativeAppIdentity(appName)
	// DevTools (Ctrl+Shift+I / F12) only when explicitly enabled.
	// NestCafe turns this on via SUPERCLI_DEVTOOLS=1 when "Szczegóły
	// diagnostyczne" is saved in webgui-settings.json.
	devtoolsEnv := strings.TrimSpace(strings.ToLower(os.Getenv("SUPERCLI_DEVTOOLS")))
	devtools := devtoolsEnv == "1" || devtoolsEnv == "true" || devtoolsEnv == "yes" || devtoolsEnv == "on"
	w, err := glaze.New(devtools)
	if err != nil {
		return fmt.Errorf("native WebView2 unavailable: %w", err)
	}
	if devtools {
		log.Printf("WebView2 DevTools enabled (Ctrl+Shift+I / F12)")
	}
	defer w.Destroy()

	w.SetTitle(appName)
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
			w.SetTitle(appName)
			return
		}
		if count > 0 {
			w.SetTitle(fmt.Sprintf("(%d) %s", count, appName))
			return
		}
		w.SetTitle(appName)
	})
	if err := w.Bind("supercliSaveFile", func(filename, encoded string) (nativeSaveResult, error) {
		if len(encoded) > 64<<20 {
			return nativeSaveResult{}, fmt.Errorf("file is too large to save")
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nativeSaveResult{}, fmt.Errorf("decode file: %w", err)
		}
		name := filepath.Base(strings.TrimSpace(filename))
		if name == "" || name == "." {
			name = "document"
		}
		extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		filters := []glaze.FileFilter{{Name: "Wszystkie pliki", Extensions: []string{"*"}}}
		if extension != "" {
			filters = []glaze.FileFilter{{Name: "Dokument " + strings.ToUpper(extension), Extensions: []string{extension}}}
		}
		path, err := w.SaveFile(glaze.FileDialogOptions{
			Title:    "Zapisz dokument — " + appName,
			Filename: name,
			Filters:  filters,
		})
		if err != nil {
			return nativeSaveResult{}, err
		}
		if path == "" {
			return nativeSaveResult{Saved: false}, nil
		}
		if filepath.Ext(path) == "" && extension != "" {
			path += "." + extension
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nativeSaveResult{}, fmt.Errorf("save file: %w", err)
		}
		return nativeSaveResult{Saved: true, Path: path}, nil
	}); err != nil {
		log.Printf("native file save unavailable: %v", err)
	}
	closeCallback, closeErr := installNativeCloseConfirmation(w.Window(), dataDir, appName, hasActiveWork)
	if closeErr != nil {
		log.Printf("native close confirmation unavailable: %v", closeErr)
	}
	setNativeWindowIcon(w.Window(), iconPath)
	width, height := nativeWindowSize()
	maxWidth, maxHeight := nativeWindowMaximum()
	windowState := loadWindowState(dataDir, width, height, maxWidth, maxHeight)
	w.SetSize(windowState.Width, windowState.Height, glaze.HintNone)
	// The web UI switches to an overlay drawer well before this minimum, so a
	// compact laptop screen keeps a useful conversation area instead of a
	// permanently docked inspector consuming half of the client width.
	w.SetSize(720, 520, glaze.HintMin)
	w.Navigate(url)
	if windowState.Maximized {
		_, _, _ = showWindowW.Call(uintptr(w.Window()), swMaximize)
	}
	w.Run()
	runtime.KeepAlive(closeCallback)
	return nil
}

func installNativeCloseConfirmation(window unsafe.Pointer, dataDir, appName string, hasActiveWork func() bool) (uintptr, error) {
	hwnd := uintptr(window)
	if hwnd == 0 {
		return 0, fmt.Errorf("window handle is empty")
	}
	var previous uintptr
	callback := syscall.NewCallback(func(messageWindow, message, wParam, lParam uintptr) uintptr {
		if message == wmClose {
			if shouldConfirmClose(dataDir, hasActiveWork) {
				text, textErr := windows.UTF16PtrFromString(
					fmt.Sprintf("Zamknąć %s?\n\nTrwające zadanie zostanie zatrzymane.", appName),
				)
				title, titleErr := windows.UTF16PtrFromString(appName)
				if textErr == nil && titleErr == nil {
					result, _, _ := messageBoxW.Call(
						messageWindow,
						uintptr(unsafe.Pointer(text)),
						uintptr(unsafe.Pointer(title)),
						mbYesNo|mbIconWarning|mbDefButton2,
					)
					if result != idYes {
						return 0
					}
				}
			}
			if err := saveNativeWindowSize(messageWindow, dataDir); err != nil {
				log.Printf("native window size save failed: %v", err)
			}
		}
		result, _, _ := callWindowProcW.Call(previous, messageWindow, message, wParam, lParam)
		return result
	})
	previous, _, _ = setWindowLongW.Call(hwnd, ^uintptr(3), callback)
	if previous == 0 {
		return 0, fmt.Errorf("SetWindowLongPtrW failed")
	}
	return callback, nil
}

func nativeWindowSize() (int, int) {
	maxWidth, maxHeight := nativeWindowMaximum()
	width, height := 1440, 900
	if width > maxWidth {
		width = maxWidth
	}
	if height > maxHeight {
		height = maxHeight
	}
	return width, height
}

func nativeWindowMaximum() (int, int) {
	width, height := 1440, 900
	screenWidth, _, _ := getSystemMetricsW.Call(smCXScreen)
	screenHeight, _, _ := getSystemMetricsW.Call(smCYScreen)
	if screenWidth > 0 {
		width = int(screenWidth) - 64
	}
	if screenHeight > 0 {
		height = int(screenHeight) - 96
	}
	if width < 720 {
		width = 720
	}
	if height < 520 {
		height = 520
	}
	return width, height
}

func saveNativeWindowSize(hwnd uintptr, dataDir string) error {
	placement := nativeWindowPlacement{Length: uint32(unsafe.Sizeof(nativeWindowPlacement{}))}
	ok, _, callErr := getWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&placement)))
	if ok == 0 {
		return fmt.Errorf("GetWindowPlacement: %v", callErr)
	}
	width := int(placement.NormalPosition.Right - placement.NormalPosition.Left)
	height := int(placement.NormalPosition.Bottom - placement.NormalPosition.Top)
	return saveWindowState(dataDir, width, height, placement.ShowCommand == swMaximize)
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

func setNativeAppIdentity(appName string) {
	id, err := windows.UTF16PtrFromString(appName + ".Desktop")
	if err == nil {
		_, _, _ = setAppID.Call(uintptr(unsafe.Pointer(id)))
	}
}

func setNativeWindowIcon(window unsafe.Pointer, iconPath string) {
	var icon uintptr
	if strings.TrimSpace(iconPath) != "" {
		if path, err := windows.UTF16PtrFromString(iconPath); err == nil {
			icon, _, _ = loadImageW.Call(0, uintptr(unsafe.Pointer(path)), imageIcon, 0, 0, lrDefaultSize|lrLoadFromFile)
		}
	}
	if icon == 0 {
		instance, _, _ := getModuleW.Call(0)
		icon, _, _ = loadImageW.Call(instance, appIconID, imageIcon, 0, 0, lrDefaultSize|lrShared)
	}
	if icon == 0 {
		return
	}
	_, _, _ = sendMessageW.Call(uintptr(window), wmSetIcon, iconBig, icon)
	_, _, _ = sendMessageW.Call(uintptr(window), wmSetIcon, iconSmall, icon)
	// Explorer's taskbar context menu can use the Win32 class icon instead of
	// WM_GETICON. Set both so the small icon beside "NestCafe.exe" cannot fall
	// back to the engine's former SuperCli icon.
	bigClassIndex := int32(gclpHIcon)
	smallClassIndex := int32(gclpHIconSmall)
	_, _, _ = setClassLongW.Call(uintptr(window), uintptr(bigClassIndex), icon)
	_, _, _ = setClassLongW.Call(uintptr(window), uintptr(smallClassIndex), icon)
}
