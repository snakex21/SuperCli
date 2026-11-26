//go:build windows

package childproc

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func sweepOrphanedGit(match func(string) bool) int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	type proc struct{ pid, parent uint32 }
	alive := map[uint32]bool{}
	var gits []proc
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		alive[entry.ProcessID] = true
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "git.exe") {
			gits = append(gits, proc{entry.ProcessID, entry.ParentProcessID})
		}
	}
	killed := 0
	for _, g := range gits {
		if alive[g.parent] {
			continue // its owner still runs it
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, g.pid)
		if err != nil {
			continue
		}
		if line, ok := processCommandLine(h); ok && match(line) {
			if windows.TerminateProcess(h, 1) == nil {
				killed++
			}
		}
		windows.CloseHandle(h)
	}
	return killed
}

// processCommandLine reads a process's command line (Windows 8.1+).
func processCommandLine(h windows.Handle) (string, bool) {
	buf := make([]byte, 4096)
	for {
		var size uint32
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &size)
		if err == nil {
			str := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
			return str.String(), true
		}
		if size > uint32(len(buf)) && size < 1<<20 {
			buf = make([]byte, size)
			continue
		}
		return "", false
	}
}
