package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"supercli/internal/system/childproc"
)

const screenshotCaptureTimeout = 15 * time.Second

type captureBuffer struct {
	buffer    *bytes.Buffer
	remaining int
	ctx       context.Context
}

func (w *captureBuffer) Write(p []byte) (int, error) {
	if w.ctx != nil {
		if err := w.ctx.Err(); err != nil {
			return 0, err
		}
	}
	if len(p) > w.remaining {
		return 0, fmt.Errorf("capture output exceeds limit")
	}
	n, err := w.buffer.Write(p)
	w.remaining -= n
	return n, err
}

func captureCommand(ctx context.Context, cmd *exec.Cmd, limit int) ([]byte, error) {
	childproc.HideWindow(cmd)
	var output, stderr bytes.Buffer
	cmd.Stdout = &captureBuffer{buffer: &output, remaining: limit, ctx: ctx}
	cmd.Stderr = &captureBuffer{buffer: &stderr, remaining: 4096, ctx: ctx}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w: %s", filepathName(cmd.Path), err, strings.TrimSpace(stderr.String()))
	}
	return output.Bytes(), nil
}

func filepathName(path string) string {
	i := strings.LastIndexAny(path, "/\\")
	return path[i+1:]
}

func readCapturedPNG(ctx context.Context, f *os.File) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > DefaultMaxScreenshotBytes {
		return nil, "", fmt.Errorf("capture file exceeds limit or is not regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, DefaultMaxScreenshotBytes+1))
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, "", err
	}
	if len(data) > DefaultMaxScreenshotBytes {
		return nil, "", fmt.Errorf("capture output exceeds limit")
	}
	if sniffMediaType(data) != "image/png" {
		return nil, "", fmt.Errorf("capture helper did not produce PNG")
	}
	return data, "image/png", nil
}
