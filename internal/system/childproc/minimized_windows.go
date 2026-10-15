//go:build windows

package childproc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StartMinimized launches cmd with its first GUI window shown minimized and
// without activation (SW_SHOWMINNOACTIVE, like "start /min"): the program
// never covers or takes focus from the user's work, yet stays on the taskbar
// and remains reachable for accessibility and capture. Go's exec package can
// only request SW_HIDE, so the process is created directly.
//
// cmd.Stdin/Stdout/Stderr must be *os.File or nil (NUL). cmd is not started
// through exec: wait on cmd.Process, never cmd.Wait. The process starts
// suspended and joins its kill-on-close job before its first instruction.
func StartMinimized(cmd *exec.Cmd) (*Scope, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	if cmd.Process != nil {
		return nil, fmt.Errorf("exec: already started")
	}
	path := cmd.Path
	if !filepath.IsAbs(path) {
		if cmd.Dir != "" {
			if candidate := filepath.Join(cmd.Dir, path); fileExists(candidate) {
				path = candidate
			}
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	app, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return nil, err
	}
	var dir *uint16
	if cmd.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return nil, err
		}
	}
	var env []uint16
	for _, pair := range cmd.Environ() {
		value, err := windows.UTF16FromString(pair)
		if err != nil {
			return nil, err
		}
		env = append(env, value...)
	}
	env = append(env, 0)
	if len(env) == 1 {
		env = append(env, 0)
	}

	current := windows.CurrentProcess()
	var inherited [3]windows.Handle
	defer func() {
		for _, h := range inherited {
			if h != 0 {
				_ = windows.CloseHandle(h)
			}
		}
	}()
	for i, stream := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
		file, ok := stream.(*os.File)
		if stream != nil && !ok {
			return nil, fmt.Errorf("minimized start needs file streams")
		}
		if file == nil {
			if file, err = os.OpenFile(os.DevNull, os.O_RDWR, 0); err != nil {
				return nil, err
			}
			defer file.Close()
		}
		// Inheritable duplicates exist only for this call and are listed
		// explicitly, so no unrelated handle leaks into the child.
		if err := windows.DuplicateHandle(current, windows.Handle(file.Fd()), current, &inherited[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0])); err != nil {
		return nil, err
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW
	startup.ShowWindow = windows.SW_SHOWMINNOACTIVE
	startup.StdInput, startup.StdOutput, startup.StdErr = inherited[0], inherited[1], inherited[2]
	flags := uint32(createNoWindow | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	var info windows.ProcessInformation
	if err := windows.CreateProcess(app, line, nil, nil, true, flags, &env[0], dir, &startup.StartupInfo, &info); err != nil {
		return nil, &os.PathError{Op: "fork/exec", Path: path, Err: err}
	}
	defer windows.CloseHandle(info.Thread)
	defer windows.CloseHandle(info.Process)
	s := &Scope{job: killOnCloseJob(info.Process)}
	// The creation handle still pins this PID, so FindProcess opens this child.
	process, err := os.FindProcess(int(info.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		_ = s.Close()
		return nil, err
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		_ = process.Release()
		_ = s.Close()
		return nil, err
	}
	cmd.Process = process
	journalChild(cmd)
	return s, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
