//go:build windows

package media

import (
	"bytes"
	"context"
	"fmt"
)

// captureScreen starts no process, watcher or permanent capture service.
func captureScreen(ctx context.Context, _ string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	restore := enterCaptureDPI()
	defer restore()
	metric := captureUser32.NewProc("GetSystemMetrics")
	getMetric := func(n uintptr) int32 { v, _, _ := metric.Call(n); return int32(v) }
	x, y, width, height := getMetric(76), getMetric(77), getMetric(78), getMetric(79)
	var output bytes.Buffer
	err := writeWindowsBitmap(ctx, width, height, &output, func(dc, mem uintptr, _ []byte) error {
		ok, _, err := captureGDI32.NewProc("BitBlt").Call(mem, 0, 0, uintptr(width), uintptr(height), dc, uintptr(x), uintptr(y), 0x00CC0020|0x40000000)
		if ok == 0 {
			return fmt.Errorf("screen capture: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return output.Bytes(), "image/png", nil
}
