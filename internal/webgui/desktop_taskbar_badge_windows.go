//go:build windows

package webgui

import (
	"fmt"
	"log"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

var (
	taskbarCLSID = ole.NewGUID("{56FDF344-FD6D-11D0-958A-006097C9A090}")
	taskbarIID3  = ole.NewGUID("{EA1AFB91-9E28-4B86-90E9-9E9F8A5EEFAF}")

	gdi32DLL            = windows.NewLazySystemDLL("gdi32.dll")
	createDIBSection    = gdi32DLL.NewProc("CreateDIBSection")
	createBitmap        = gdi32DLL.NewProc("CreateBitmap")
	deleteObject        = gdi32DLL.NewProc("DeleteObject")
	createIconIndirect  = user32DLL.NewProc("CreateIconIndirect")
	destroyIcon         = user32DLL.NewProc("DestroyIcon")
	getForegroundWindow = user32DLL.NewProc("GetForegroundWindow")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

type nativeTaskbarBadge struct {
	hwnd     uintptr
	commands chan badgeCommand
	stop     chan struct{}
	done     chan struct{}
}

type badgeCommand uint8

const (
	badgeClear badgeCommand = iota
	badgeCompleted
)

func newNativeTaskbarBadge(hwnd uintptr) (*nativeTaskbarBadge, error) {
	b := &nativeTaskbarBadge{
		hwnd: hwnd, commands: make(chan badgeCommand, 64), stop: make(chan struct{}), done: make(chan struct{}),
	}
	ready := make(chan error, 1)
	go b.run(ready)
	if err := <-ready; err != nil {
		<-b.done
		return nil, err
	}
	return b, nil
}

func (b *nativeTaskbarBadge) Set(count int) {
	// Positive counts are authoritative on the native backend. WebView focus
	// reporting is unreliable when another desktop window covers WebView2, but
	// a zero from the frontend is still a useful immediate acknowledgement.
	if count != 0 {
		return
	}
	b.send(badgeClear)
}

func (b *nativeTaskbarBadge) Completed() {
	b.send(badgeCompleted)
}

func (b *nativeTaskbarBadge) send(command badgeCommand) {
	select {
	case b.commands <- command:
	default:
		// Sixty-four unseen completions already render as 9+; avoid blocking the
		// HTTP completion path if Explorer is temporarily stalled.
	}
}

func (b *nativeTaskbarBadge) Close() {
	close(b.stop)
	<-b.done
}

func (b *nativeTaskbarBadge) run(ready chan<- error) {
	defer close(b.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	initialized := false
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err == nil {
		initialized = true
	} else if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 1 { // S_FALSE still needs CoUninitialize.
		ready <- fmt.Errorf("initialize taskbar COM: %w", err)
		return
	} else {
		initialized = true
	}
	if initialized {
		defer ole.CoUninitialize()
	}

	taskbar, err := ole.CreateInstance(taskbarCLSID, taskbarIID3)
	if err != nil {
		ready <- fmt.Errorf("create ITaskbarList3: %w", err)
		return
	}
	defer taskbar.Release()
	methods := (*[21]uintptr)(unsafe.Pointer(taskbar.RawVTable))
	if hr, _, _ := syscall.SyscallN(methods[3], uintptr(unsafe.Pointer(taskbar))); int32(hr) < 0 {
		ready <- fmt.Errorf("ITaskbarList3.HrInit: HRESULT 0x%08X", uint32(hr))
		return
	}
	ready <- nil
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	count := 0
	apply := func(next int) {
		if next == count {
			return
		}
		if err := setTaskbarOverlay(taskbar, methods[18], b.hwnd, next); err != nil {
			log.Printf("native taskbar badge update %d failed: %v", next, err)
			return
		}
		count = next
		log.Printf("native taskbar badge count=%d", count)
	}

	for {
		select {
		case command := <-b.commands:
			switch command {
			case badgeClear:
				apply(0)
			case badgeCompleted:
				foreground, _, _ := getForegroundWindow.Call()
				if foreground == b.hwnd {
					apply(0)
				} else {
					apply(count + 1)
				}
			}
		case <-ticker.C:
			if count > 0 {
				foreground, _, _ := getForegroundWindow.Call()
				if foreground == b.hwnd {
					apply(0)
				}
			}
		case <-b.stop:
			apply(0)
			return
		}
	}
}

func setTaskbarOverlay(taskbar *ole.IUnknown, method, hwnd uintptr, count int) error {
	var icon uintptr
	var err error
	if count > 0 {
		icon, err = createTaskbarBadgeIcon(count)
		if err != nil {
			return err
		}
		defer destroyIcon.Call(icon)
	}
	var description uintptr
	if count > 0 {
		text, convErr := windows.UTF16PtrFromString(fmt.Sprintf("SuperCli: %d nieprzeczytane", count))
		if convErr == nil {
			description = uintptr(unsafe.Pointer(text))
		}
	}
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(taskbar)), hwnd, icon, description)
	if int32(hr) < 0 {
		return fmt.Errorf("ITaskbarList3.SetOverlayIcon: HRESULT 0x%08X", uint32(hr))
	}
	return nil
}

func createTaskbarBadgeIcon(count int) (uintptr, error) {
	const size = 16
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: size, Height: -size,
		Planes: 1, BitCount: 32, SizeImage: size * size * 4,
	}}
	var bits unsafe.Pointer
	color, _, callErr := createDIBSection.Call(0, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == nil {
		return 0, fmt.Errorf("CreateDIBSection: %v", callErr)
	}
	defer deleteObject.Call(color)
	pixels := taskbarBadgePixels(count)
	copy(unsafe.Slice((*byte)(bits), len(pixels)), pixels)

	mask, _, callErr := createBitmap.Call(size, size, 1, 1, 0)
	if mask == 0 {
		return 0, fmt.Errorf("CreateBitmap: %v", callErr)
	}
	defer deleteObject.Call(mask)
	icon, _, callErr := createIconIndirect.Call(uintptr(unsafe.Pointer(&iconInfo{FIcon: 1, HbmMask: mask, HbmColor: color})))
	if icon == 0 {
		return 0, fmt.Errorf("CreateIconIndirect: %v", callErr)
	}
	return icon, nil
}
