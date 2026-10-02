//go:build windows

package media

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// captureScreen uses one bounded top-down DIB, encoded directly to PNG.
// It starts no process, watcher or permanent capture service.
func captureScreen(ctx context.Context, _ string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user := windows.NewLazySystemDLL("user32.dll")
	gdi := windows.NewLazySystemDLL("gdi32.dll")
	dpi := user.NewProc("SetThreadDpiAwarenessContext")
	if dpi.Find() == nil {
		old, _, _ := dpi.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4).
		if old != 0 {
			defer dpi.Call(old)
		}
	}
	metric := user.NewProc("GetSystemMetrics")
	getMetric := func(n uintptr) int32 { v, _, _ := metric.Call(n); return int32(v) }
	x, y, width, height := getMetric(76), getMetric(77), getMetric(78), getMetric(79)
	if width <= 0 || height <= 0 || width > 32768 || height > 32768 || int64(width)*int64(height) > 32<<20 {
		return nil, "", fmt.Errorf("screen dimensions unavailable or exceed 32 megapixels")
	}
	dc, _, err := user.NewProc("GetDC").Call(0)
	if dc == 0 {
		return nil, "", fmt.Errorf("screen device context: %v", err)
	}
	defer user.NewProc("ReleaseDC").Call(0, dc)
	mem, _, err := gdi.NewProc("CreateCompatibleDC").Call(dc)
	if mem == 0 {
		return nil, "", fmt.Errorf("screen memory context: %v", err)
	}
	defer gdi.NewProc("DeleteDC").Call(mem)
	// BITMAPINFOHEADER followed by the optional RGBQUAD slot.
	info := struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		Used, Important, Color uint32
	}{Size: 40, Width: width, Height: -height, Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	bitmap, _, err := gdi.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return nil, "", fmt.Errorf("screen bitmap: %v", err)
	}
	defer gdi.NewProc("DeleteObject").Call(bitmap)
	old, _, err := gdi.NewProc("SelectObject").Call(mem, bitmap)
	if old == 0 || old == ^uintptr(0) {
		return nil, "", fmt.Errorf("select screen bitmap: %v", err)
	}
	defer gdi.NewProc("SelectObject").Call(mem, old)
	ok, _, err := gdi.NewProc("BitBlt").Call(mem, 0, 0, uintptr(width), uintptr(height), dc, uintptr(x), uintptr(y), 0x00CC0020|0x40000000)
	if ok == 0 {
		return nil, "", fmt.Errorf("screen capture: %v", err)
	}
	ok, _, err = gdi.NewProc("GdiFlush").Call()
	if ok == 0 {
		return nil, "", fmt.Errorf("screen capture flush: %v", err)
	}
	pixels := unsafe.Slice((*byte)(bits), int(width)*int(height)*4)
	for row := 0; row < int(height); row++ {
		if row%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
		}
		for i := row * int(width) * 4; i < (row+1)*int(width)*4; i += 4 {
			pixels[i], pixels[i+2], pixels[i+3] = pixels[i+2], pixels[i], 255
		}
	}
	img := &image.NRGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
	var output bytes.Buffer
	writer := &captureBuffer{buffer: &output, remaining: DefaultMaxScreenshotBytes, ctx: ctx}
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(writer, img); err != nil {
		return nil, "", err
	}
	return output.Bytes(), "image/png", nil
}
