//go:build !windows

package windowautomation

import (
	"context"
	"fmt"
)

func Open(int, uint64) (*Process, error) {
	return nil, fmt.Errorf("native window accessibility is currently supported on Windows only")
}
func (*Process) Close() {}
func (*Process) Alive() error {
	return fmt.Errorf("native window accessibility is currently supported on Windows only")
}
func executeNative(context.Context, Request) (Result, error) {
	return Result{}, fmt.Errorf("native window accessibility is currently supported on Windows only")
}
