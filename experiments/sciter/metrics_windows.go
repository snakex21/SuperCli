//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

type metrics struct {
	WorkingSet, Private, PrivateWorkingSet uint64
	CPUMS                                  int64
}
type memCounters struct {
	Size, PageFaults                                                                                      uint32
	PeakWorkingSet, WorkingSet, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private uintptr
}
type fileTime struct{ Low, High uint32 }

func (f fileTime) ticks() uint64 { return uint64(f.High)<<32 | uint64(f.Low) }
func processMetrics(pid int) metrics {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	handle, _, _ := kernel.NewProc("GetCurrentProcess").Call()
	if pid != 0 {
		handle, _, _ = kernel.NewProc("OpenProcess").Call(0x410, 0, uintptr(pid))
		defer kernel.NewProc("CloseHandle").Call(handle)
	}
	var memory memCounters
	memory.Size = uint32(unsafe.Sizeof(memory))
	syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo").Call(handle, uintptr(unsafe.Pointer(&memory)), uintptr(memory.Size))
	var created, exited, kernelTime, userTime fileTime
	kernel.NewProc("GetProcessTimes").Call(handle, uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&exited)), uintptr(unsafe.Pointer(&kernelTime)), uintptr(unsafe.Pointer(&userTime)))
	var pages = make([]uintptr, 2+int(memory.WorkingSet)/4096+4096)
	for attempt := 0; attempt < 3; attempt++ {
		ok, _, _ := syscall.NewLazyDLL("psapi.dll").NewProc("QueryWorkingSet").Call(handle, uintptr(unsafe.Pointer(&pages[0])), uintptr(len(pages))*unsafe.Sizeof(pages[0]))
		if ok != 0 {
			var privateWS uint64
			count := int(pages[0])
			if count >= len(pages) {
				break
			}
			for _, page := range pages[1 : count+1] {
				if page&0x100 == 0 {
					privateWS += 4096
				}
			}
			return metrics{WorkingSet: uint64(memory.WorkingSet), Private: uint64(memory.Private), PrivateWorkingSet: privateWS, CPUMS: int64((kernelTime.ticks() + userTime.ticks()) / 10000)}
		}
		pages = make([]uintptr, len(pages)*2)
	}
	return metrics{WorkingSet: uint64(memory.WorkingSet), Private: uint64(memory.Private), CPUMS: int64((kernelTime.ticks() + userTime.ticks()) / 10000)}
}

func closeProcessWindow(pid int) {
	user := syscall.NewLazyDLL("user32.dll")
	cb := syscall.NewCallback(func(hwnd, unused uintptr) uintptr {
		var found uint32
		user.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&found)))
		if int(found) == pid {
			user.NewProc("PostMessageW").Call(hwnd, 0x10, 0, 0)
		}
		return 1
	})
	user.NewProc("EnumWindows").Call(cb, 0)
}

type processEntry struct {
	Size, Usage, PID        uint32
	Heap                    uintptr
	Module, Threads, Parent uint32
	Priority                int32
	Flags                   uint32
	Exe                     [260]uint16
}

func processTreeMetrics(parent int) metrics {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	snapshot, _, _ := kernel.NewProc("CreateToolhelp32Snapshot").Call(2, 0)
	defer kernel.NewProc("CloseHandle").Call(snapshot)
	entry := processEntry{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	parents := map[uint32]uint32{}
	ok, _, _ := kernel.NewProc("Process32FirstW").Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		parents[entry.PID] = entry.Parent
		ok, _, _ = kernel.NewProc("Process32NextW").Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	owned := map[uint32]bool{uint32(parent): true}
	for added := true; added; {
		added = false
		for pid, ppid := range parents {
			if owned[ppid] && !owned[pid] {
				owned[pid] = true
				added = true
			}
		}
	}
	var result metrics
	for pid := range owned {
		m := processMetrics(int(pid))
		result.WorkingSet += m.WorkingSet
		result.Private += m.Private
		result.PrivateWorkingSet += m.PrivateWorkingSet
		result.CPUMS += m.CPUMS
	}
	return result
}
func syscallDPI() {
	syscall.NewLazyDLL("user32.dll").NewProc("SetProcessDpiAwarenessContext").Call(^uintptr(3))
}

func comparisonWindowSize() (int, int) {
	user := syscall.NewLazyDLL("user32.dll")
	dpi, _, _ := user.NewProc("GetDpiForSystem").Call()
	if dpi == 0 {
		dpi = 96
	}
	rect := struct{ Left, Top, Right, Bottom int32 }{Right: int32(1200 * dpi / 96), Bottom: int32(820 * dpi / 96)}
	user.NewProc("AdjustWindowRectExForDpi").Call(uintptr(unsafe.Pointer(&rect)), 0xCF0000, 0, 0, dpi)
	return int(rect.Right - rect.Left), int(rect.Bottom - rect.Top)
}

func showComparisonWindow(hwnd uintptr) {
	u := syscall.NewLazyDLL("user32.dll")
	u.NewProc("ShowWindow").Call(hwnd, 1)
	u.NewProc("SetForegroundWindow").Call(hwnd)
}
