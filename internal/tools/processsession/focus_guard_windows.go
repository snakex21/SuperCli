//go:build windows

package processsession

import (
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A program driven through UI Automation can activate itself or open a modal
// dialog (for example "Save changes?") that takes the foreground and with it
// the user's keyboard. guardForeground watches one agent action plus a short
// grace period and hands the foreground back to the window the user had. It
// never sends input. The brief AttachThreadInput is needed because Windows
// otherwise refuses SetForegroundWindow from a background process.
// Limitation: if the user deliberately switches to the target program during
// the grace period, the foreground is returned once more.
const focusGrace = 1500 * time.Millisecond

var (
	focusUser32   = windows.NewLazySystemDLL("user32.dll")
	focusKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	focusGuards   sync.Map // hook handle -> *focusGuard
	// One process-wide callback: Go limits the number of NewCallback values.
	focusCallback = windows.NewCallback(func(hook, _, _, _, _, _, _ uintptr) uintptr {
		if g, ok := focusGuards.Load(hook); ok {
			g.(*focusGuard).check()
		}
		return 0
	})
)

type focusGuard struct {
	pid    uint32
	mu     sync.Mutex
	before uintptr
}

func guardForeground(pid int) func() {
	before, _, _ := focusUser32.NewProc("GetForegroundWindow").Call()
	if pid <= 0 || before == 0 || focusWindowPID(before) == uint32(pid) {
		// Nothing to protect, or the user is already working in that program.
		return func() {}
	}
	g := &focusGuard{pid: uint32(pid), before: before}
	ready := make(chan uint32, 1)
	go g.watch(ready)
	thread := <-ready
	return func() {
		g.check()
		time.AfterFunc(focusGrace, func() {
			g.check()
			if thread != 0 {
				focusUser32.NewProc("PostThreadMessageW").Call(uintptr(thread), 0x0012, 0, 0) // WM_QUIT
			}
		})
	}
}

// watch delivers EVENT_SYSTEM_FOREGROUND for all processes, so a switch the
// user makes to another program also updates the window to return to.
func (g *focusGuard) watch(ready chan<- uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hook, _, _ := focusUser32.NewProc("SetWinEventHook").Call(0x0003, 0x0003, 0, focusCallback, 0, 0, 0) // WINEVENT_OUTOFCONTEXT
	if hook == 0 {
		ready <- 0
		return
	}
	defer focusUser32.NewProc("UnhookWinEvent").Call(hook)
	focusGuards.Store(hook, g)
	defer focusGuards.Delete(hook)
	var msg [48]byte
	// Create the message queue before the thread id is published.
	focusUser32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0, 0)
	thread, _, _ := focusKernel32.NewProc("GetCurrentThreadId").Call()
	ready <- uint32(thread)
	for {
		r, _, _ := focusUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		focusUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg[0])))
	}
}

func (g *focusGuard) check() {
	g.mu.Lock()
	defer g.mu.Unlock()
	current, _, _ := focusUser32.NewProc("GetForegroundWindow").Call()
	if current == 0 {
		return
	}
	if focusWindowPID(current) != g.pid {
		g.before = current
		return
	}
	if alive, _, _ := focusUser32.NewProc("IsWindow").Call(g.before); alive == 0 {
		return
	}
	restoreForeground(current, g.before)
}

func restoreForeground(stolen, before uintptr) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	self, _, _ := focusKernel32.NewProc("GetCurrentThreadId").Call()
	other, _, _ := focusUser32.NewProc("GetWindowThreadProcessId").Call(stolen, 0)
	if other != 0 && other != self {
		if ok, _, _ := focusUser32.NewProc("AttachThreadInput").Call(self, other, 1); ok != 0 {
			defer focusUser32.NewProc("AttachThreadInput").Call(self, other, 0)
		}
	}
	if ok, _, _ := focusUser32.NewProc("SetForegroundWindow").Call(before); ok == 0 {
		focusUser32.NewProc("BringWindowToTop").Call(before)
		focusUser32.NewProc("SetForegroundWindow").Call(before)
	}
}

func focusWindowPID(hwnd uintptr) uint32 {
	var pid uint32
	focusUser32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}
