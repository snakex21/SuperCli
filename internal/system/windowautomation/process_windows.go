//go:build windows

package windowautomation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"supercli/internal/system/childproc"
)

func Open(pid int, expected uint64) (*Process, error) {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("invalid process_id")
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("open window process: %w", err)
	}
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	stamp := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	if expected != 0 && stamp != expected {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("process identity changed; the PID was reused")
	}
	name := make([]uint16, 32768)
	size := uint32(len(name))
	if err = windows.QueryFullProcessImageName(h, 0, &name[0], &size); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	p := &Process{Identity: Identity{PID: pid, Created: stamp}, Name: windows.UTF16ToString(name[:size]), handle: uintptr(h)}
	if err = p.Alive(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *Process) Close() {
	if p != nil && p.handle != 0 {
		windows.CloseHandle(windows.Handle(p.handle))
		p.handle = 0
	}
}

func (p *Process) Alive() error {
	if p == nil || p.handle == 0 {
		return fmt.Errorf("process handle is closed")
	}
	state, err := windows.WaitForSingleObject(windows.Handle(p.handle), 0)
	if err != nil {
		return err
	}
	if state == uint32(windows.WAIT_TIMEOUT) {
		return nil
	}
	var code uint32
	if err := windows.GetExitCodeProcess(windows.Handle(p.handle), &code); err != nil {
		return err
	}
	return fmt.Errorf("process has exited (code %d)", code)
}

const helperArgument = "--supercli-internal-window-accessibility"
const helperEnvironment = "SUPERCLI_INTERNAL_WINDOW_ACCESSIBILITY"

// Accessibility providers can hang inside RPC. Isolate COM in a hidden helper
// whose deadline can terminate it; never strand the agent's UI thread or poll.
func init() {
	if len(os.Args) != 2 || os.Args[1] != helperArgument || os.Getenv(helperEnvironment) != "native-uia-v1" {
		return
	}
	if err := runHelper(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runHelper(input io.Reader, output io.Writer) error {
	var r Request
	decoder := json.NewDecoder(io.LimitReader(input, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing accessibility request")
	}
	if err := Validate(r); err != nil {
		return err
	}
	if r.Created == 0 {
		return fmt.Errorf("native accessibility requires a pinned process identity")
	}
	p, err := Open(r.PID, r.Created)
	if err != nil {
		return err
	}
	defer p.Close()
	result, err := runUIAutomation(r, p)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > b.remaining {
		return 0, fmt.Errorf("native accessibility output exceeded its limit")
	}
	b.remaining -= len(data)
	return b.Buffer.Write(data)
}

func executeNative(ctx context.Context, r Request) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return Result{}, err
	}
	cmd := exec.CommandContext(ctx, exe, helperArgument)
	childproc.HideWindow(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(body)
	for _, pair := range os.Environ() {
		if !strings.EqualFold(strings.SplitN(pair, "=", 2)[0], helperEnvironment) {
			cmd.Env = append(cmd.Env, pair)
		}
	}
	cmd.Env = append(cmd.Env, helperEnvironment+"=native-uia-v1")
	stdout, stderr := &limitedBuffer{remaining: 512 << 10}, &limitedBuffer{remaining: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("accessibility action interrupted; a dispatched action may already have taken effect: %w", ctx.Err())
		}
		return Result{}, fmt.Errorf("native accessibility: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return Result{}, err
	}
	if result.PID != r.PID {
		return Result{}, fmt.Errorf("accessibility helper returned a different process")
	}
	return result, nil
}
