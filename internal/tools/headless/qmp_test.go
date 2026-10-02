package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools/core"
)

func qmpFixture(t *testing.T, handler func(map[string]json.RawMessage, net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fixture connection not reaped")
		}
	})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		io.WriteString(conn, "{\"QMP\":{\"version\":{\"qemu\":{\"major\":8,\"minor\":2,\"micro\":0}},\"capabilities\":[]}}\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request map[string]json.RawMessage
			if err := json.Unmarshal(line, &request); err != nil {
				t.Error(err)
				return
			}
			var command string
			_ = json.Unmarshal(request["execute"], &command)
			if command == "qmp_capabilities" {
				fmt.Fprintf(conn, "{\"return\":{},\"id\":%s}\r\n", request["id"])
				continue
			}
			handler(request, conn)
		}
	}()
	return "tcp://" + listener.Addr().String()
}
func fixturePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 128, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 128; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func executeQMP(t *testing.T, tool *Tool, endpoint, action string, extra map[string]any) core.Result {
	t.Helper()
	p := map[string]any{"protocol": "qmp", "endpoint": endpoint, "action": action}
	for k, v := range extra {
		p[k] = v
	}
	data, _ := json.Marshal(p)
	result, err := tool.Execute(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestQMPCaptureDoesNotTouchDesktopAndPreservesPixels(t *testing.T) {
	pixels := fixturePNG(t)
	calls := make(chan string, 4)
	endpoint := qmpFixture(t, func(request map[string]json.RawMessage, conn net.Conn) {
		var command string
		_ = json.Unmarshal(request["execute"], &command)
		calls <- command
		if command != "screendump" {
			t.Errorf("unexpected %s", command)
			return
		}
		var args struct{ Filename, Format string }
		_ = json.Unmarshal(request["arguments"], &args)
		if args.Format != "png" {
			t.Error("missing explicit PNG")
		}
		if err := os.WriteFile(args.Filename, pixels, 0600); err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(conn, "{\"event\":\"RESUME\"}\r\n{\"return\":{},\"id\":%s}\r\n", request["id"])
	})
	tool := New(t.TempDir(), t.TempDir())
	for _, attach := range []bool{false, true} {
		result := executeQMP(t, tool, endpoint, "screenshot", map[string]any{"attach": attach})
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		var metadata struct {
			Path        string `json:"path"`
			PreviewPath string `json:"preview_path"`
			Source      string `json:"source"`
			Attached    bool   `json:"attached"`
		}
		if err := json.Unmarshal([]byte(result.Text), &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.Source != "qmp" || metadata.Attached != attach || !strings.HasPrefix(metadata.PreviewPath, "snapshot:") {
			t.Fatalf("metadata %s", result.Text)
		}
		saved, err := os.ReadFile(metadata.Path)
		if err != nil || !bytes.Equal(saved, pixels) {
			t.Fatal("original changed", err)
		}
		if (result.Image != nil) != attach {
			t.Fatal("unexpected image upload")
		}
		if attach && !bytes.Equal(result.Image.Data, pixels) {
			t.Fatal("small pixels changed")
		}
		if command := <-calls; command != "screendump" {
			t.Fatal(command)
		}
		break // A fixture represents one attached QMP connection; next test covers attachment.
	}
}
func TestQMPAttachedAnalysisAndTemporaryCleanup(t *testing.T) {
	pixels := fixturePNG(t)
	dataDir := t.TempDir()
	endpoint := qmpFixture(t, func(request map[string]json.RawMessage, conn net.Conn) {
		var args struct{ Filename string }
		_ = json.Unmarshal(request["arguments"], &args)
		if err := os.WriteFile(args.Filename, pixels, 0600); err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(conn, "{\"return\":{},\"id\":%s}\n", request["id"])
	})
	result := executeQMP(t, New(t.TempDir(), dataDir), endpoint, "screenshot", map[string]any{"attach": true})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Image == nil || !bytes.Equal(result.Image.Data, pixels) {
		t.Fatal("missing analysis")
	}
	entries, err := os.ReadDir(dataDir + "/.supercli/headless-capture")
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary capture retained", err)
	}
}
func TestQMPKeyChordIsOneOrderedCommand(t *testing.T) {
	received := make(chan []map[string]any, 1)
	endpoint := qmpFixture(t, func(request map[string]json.RawMessage, conn net.Conn) {
		var command string
		_ = json.Unmarshal(request["execute"], &command)
		if command != "input-send-event" {
			t.Error(command)
		}
		var args struct{ Events []map[string]any }
		_ = json.Unmarshal(request["arguments"], &args)
		received <- args.Events
		fmt.Fprintf(conn, "{\"return\":{},\"id\":%s}\n", request["id"])
	})
	result := executeQMP(t, New(t.TempDir(), t.TempDir()), endpoint, "keys", map[string]any{"keys": []string{"ctrl", "alt", "delete"}})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	events := <-received
	if len(events) != 6 {
		t.Fatal(events)
	}
	for i, key := range []string{"ctrl", "alt", "delete", "delete", "alt", "ctrl"} {
		data := events[i]["data"].(map[string]any)
		if data["key"].(map[string]any)["data"] != key || data["down"] != (i < 3) {
			t.Fatal(events)
		}
	}
}
func TestQMPWaitReadsEventsWithoutStatusPolling(t *testing.T) {
	ready := make(chan struct{})
	done := make(chan struct{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.WriteString(conn, "{\"QMP\":{}}\n")
		var request struct {
			Execute string
			ID      int
		}
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Execute != "qmp_capabilities" {
			t.Error(request.Execute)
		}
		fmt.Fprintf(conn, "{\"return\":{},\"id\":%d}\n", request.ID)
		close(ready)
		io.WriteString(conn, "{\"event\":\"RESUME\"}\n{\"event\":\"SHUTDOWN\",\"data\":{\"guest\":true}}\n")
		var b [1]byte
		_, _ = conn.Read(b[:])
	}()
	result := executeQMP(t, New(t.TempDir(), t.TempDir()), "tcp://"+listener.Addr().String(), "wait_event", map[string]any{"event": "SHUTDOWN"})
	<-ready
	if result.Err != nil || !strings.Contains(result.Text, "SHUTDOWN") {
		t.Fatal(result)
	}
	<-done
}
func TestQMPCancelClosesOnlyControlConnection(t *testing.T) {
	ready := make(chan struct{})
	done := make(chan struct{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.WriteString(conn, "{\"QMP\":{}}\n")
		var request struct{ ID int }
		_ = json.NewDecoder(conn).Decode(&request)
		fmt.Fprintf(conn, "{\"return\":{},\"id\":%d}\n", request.ID)
		close(ready)
		var b [1]byte
		_, _ = conn.Read(b[:])
	}()
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan core.Result, 1)
	raw, _ := json.Marshal(map[string]any{"protocol": "qmp", "endpoint": "tcp://" + listener.Addr().String(), "action": "wait_event", "event": "SHUTDOWN"})
	go func() { result, _ := New(t.TempDir(), t.TempDir()).Execute(ctx, raw); resultCh <- result }()
	<-ready
	cancel()
	result := <-resultCh
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatal(result.Err)
	}
	<-done
}
func TestQMPRejectsUnframedOrOversizedResponses(t *testing.T) {
	endpoint := qmpFixture(t, func(request map[string]json.RawMessage, conn net.Conn) {
		io.WriteString(conn, strings.Repeat("x", maxQMPMessage+1)+"\n")
	})
	result := executeQMP(t, New(t.TempDir(), t.TempDir()), endpoint, "status", nil)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "64 KiB") {
		t.Fatal(result.Err)
	}
}
func TestEndpointAndActionValidationBeforeConnecting(t *testing.T) {
	for _, raw := range []string{
		`{"protocol":"qmp","endpoint":"tcp://example.com:4444","action":"status"}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"click","x":10}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"keys","keys":["CTRL"]}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"close"}`,
		`{"protocol":"qmp","endpoint":"tcp://user@127.0.0.1:4444","action":"status"}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444/x","action":"status"}`,
	} {
		result, _ := New(t.TempDir(), t.TempDir()).Execute(context.Background(), []byte(raw))
		if result.Err == nil || strings.Contains(result.Err.Error(), "connect QMP") {
			t.Fatalf("invalid args reached dial: %s: %v", raw, result.Err)
		}
	}
}
func TestTargetGatesSerializeAndCleanUp(t *testing.T) {
	release, err := lockTarget(context.Background(), "fixture-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := lockTarget(context.Background(), "fixture-b")
	if err != nil {
		t.Fatal(err)
	}
	other()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lockTarget(ctx, "fixture-a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release()
	gates.Lock()
	size := len(gates.targets)
	gates.Unlock()
	if size != 0 {
		t.Fatalf("retained %d endpoint gates", size)
	}
}
func TestDormantToolDoesNotAllocateProfileOrConnect(t *testing.T) {
	dir := t.TempDir() + "/not-created"
	tool := New(dir, dir)
	registry := core.NewRegistry()
	registry.MustRegister(tool.Spec())
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dormant tool created data", err)
	}
	if len(registry.Visible()) != 0 {
		t.Fatal("headless tool became always-on")
	}
}

func TestQMPWaitRetainsEventDuringCapabilitiesHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.WriteString(conn, "{\"QMP\":{}}\n")
		var request struct{ ID int }
		_ = json.NewDecoder(conn).Decode(&request)
		fmt.Fprintf(conn, "{\"event\":\"SHUTDOWN\",\"data\":{\"guest\":true}}\n{\"return\":{},\"id\":%d}\n", request.ID)
		var b [1]byte
		_, _ = conn.Read(b[:])
	}()
	result := executeQMP(t, New(t.TempDir(), t.TempDir()), "tcp://"+listener.Addr().String(), "wait_event", map[string]any{"event": "SHUTDOWN"})
	if result.Err != nil || !strings.Contains(result.Text, "SHUTDOWN") {
		t.Fatal(result)
	}
	<-done
}
func TestEquivalentEndpointsShareIdentity(t *testing.T) {
	for _, pair := range [][2]string{{"tcp://[0:0:0:0:0:0:0:1]:04444", "tcp://[::1]:4444"}, {"http://127.0.0.1:09515/", "http://127.0.0.1:9515"}} {
		scheme := "tcp"
		if strings.HasPrefix(pair[0], "http") {
			scheme = "http"
		}
		a, err := localEndpoint(pair[0], scheme)
		if err != nil {
			t.Fatal(err)
		}
		b, err := localEndpoint(pair[1], scheme)
		if err != nil || a.String() != b.String() {
			t.Fatal(a, b, err)
		}
	}
}
