//go:build !windows

package media

import (
	"context"
	"fmt"
	"runtime"
)

func listCaptureWindows(ctx context.Context) ([]WindowInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("background window discovery is currently supported only on Windows (this platform: %s)", runtime.GOOS)
}

func captureWindow(ctx context.Context, selector WindowSelector) ([]byte, string, WindowInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", WindowInfo{}, err
	}
	if err := validateWindowSelector(selector); err != nil {
		return nil, "", WindowInfo{}, err
	}
	return nil, "", WindowInfo{}, fmt.Errorf("background window capture is currently supported only on Windows (this platform: %s)", runtime.GOOS)
}
