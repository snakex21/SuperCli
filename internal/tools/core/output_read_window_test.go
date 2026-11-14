package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadOutputPageScalesWithModelWindow(t *testing.T) {
	for _, c := range []struct{ window, want int }{
		{0, outputReadMax}, {8_000, outputReadMax}, {32_000, outputReadMax},
		{64_000, 64_000 / 4}, {128_000, 128_000 / 4}, {140_000, outputReadLargeMax}, {1_000_000, outputReadLargeMax},
	} {
		if got := readOutputMax(WithModelWindow(context.Background(), c.window)); got != c.want {
			t.Fatalf("window %d: page %d, want %d", c.window, got, c.want)
		}
	}
	store := NewOutputStore()
	text := strings.Repeat("0123456789abcdef\n", 4096) // 68 KiB
	ctx := WithModelWindow(context.Background(), 200_000)
	preview := store.CompactContext(ctx, "ctx_execute", text)
	if !strings.Contains(preview, `"limit":32768`) {
		t.Fatalf("large-window hint does not offer a 32 KiB page: %s", preview[len(preview)-200:])
	}
	handle := preview[strings.Index(preview, "handle=")+7:]
	handle = handle[:strings.IndexAny(handle, "]\n")]
	raw, _ := json.Marshal(map[string]any{"handle": handle, "offset": 0, "limit": 1 << 20})
	res, err := store.ReadOutputTool().Fn(ctx, raw)
	if err != nil || res.Err != nil || !strings.Contains(res.Text, "bytes 0:32768 of") {
		t.Fatalf("read_output page on a large window: %v %v %.80s", err, res.Err, res.Text)
	}
	if res, _ = store.ReadOutputTool().Fn(context.Background(), raw); !strings.Contains(res.Text, "bytes 0:8192 of") {
		t.Fatalf("unknown window must keep the 8 KiB page: %.80s", res.Text)
	}
}
