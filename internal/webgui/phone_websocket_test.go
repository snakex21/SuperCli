package webgui

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func maskedFrame(op byte, fin bool, p []byte) []byte {
	first := op
	if fin {
		first |= 128
	}
	frame := []byte{first}
	switch {
	case len(p) < 126:
		frame = append(frame, 128|byte(len(p)))
	case len(p) <= 65535:
		frame = append(frame, 128|126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(p)))
	default:
		frame = append(frame, 128|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(len(p)))
	}
	mask := []byte{3, 7, 11, 19}
	frame = append(frame, mask...)
	for i, b := range p {
		frame = append(frame, b^mask[i%4])
	}
	return frame
}
func readServerFrame(r io.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := uint64(h[1] & 127)
	if n == 126 {
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > wsLimit {
		return 0, nil, fmt.Errorf("oversized server frame")
	}
	p := make([]byte, n)
	_, err := io.ReadFull(r, p)
	return h[0] & 15, p, err
}
func TestWebSocketFrames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames []byte
		want   string
		fail   bool
	}{
		{"text", maskedFrame(1, true, []byte("hello")), "hello", false},
		{"fragmented-utf8", append(maskedFrame(1, false, []byte{0xe2}), maskedFrame(0, true, []byte{0x82, 0xac})...), "€", false},
		{"unmasked", []byte{129, 0}, "", true},
		{"orphan-continuation", maskedFrame(0, true, nil), "", true},
		{"invalid-utf8", maskedFrame(1, true, []byte{255}), "", true},
		{"fragmented-ping", maskedFrame(9, false, nil), "", true},
		{"binary", maskedFrame(2, true, nil), "", true},
		{"rsv", []byte{0xc1, 0x80}, "", true},
		{"nonminimal-length", []byte{129, 254, 0, 1}, "", true},
		{"huge", []byte{129, 255, 0, 0, 0, 0, 1, 0, 0, 0}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			_ = a.SetDeadline(time.Now().Add(time.Second))
			_ = b.SetDeadline(time.Now().Add(time.Second))
			ws := &webSocket{conn: a, reader: bufio.NewReader(bytes.NewReader(tc.frames))}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if tc.fail {
					_, _, _ = readServerFrame(b)
				}
			}()
			p, err := ws.readText()
			if (err != nil) != tc.fail || string(p) != tc.want {
				t.Fatalf("payload=%q err=%v", p, err)
			}
			<-done
		})
	}
}
func TestWebSocketPingPongAndClose(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ws := &webSocket{conn: a, reader: bufio.NewReader(a)}
	done := make(chan error, 1)
	go func() { _, err := ws.readText(); done <- err }()
	if _, err := b.Write(maskedFrame(9, true, []byte("ping"))); err != nil {
		t.Fatal(err)
	}
	op, p, err := readServerFrame(b)
	if err != nil || op != 10 || string(p) != "ping" {
		t.Fatalf("pong: %d %s %v", op, p, err)
	}
	if _, err := b.Write(maskedFrame(8, true, []byte{3, 232})); err != nil {
		t.Fatal(err)
	}
	op, _, err = readServerFrame(b)
	if err != nil || op != 8 {
		t.Fatal("missing close")
	}
	if err = <-done; err != io.EOF {
		t.Fatal(err)
	}
}
func TestPhoneWebSocketStreamsChat(t *testing.T) {
	s := newTestServer(t, false)
	setupTestPhone(t, s)
	cookie := pairTestPhone(t, s)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET /api/chat/ws HTTP/1.1\r\nHost: test-phone.trycloudflare.com\r\nOrigin: https://test-phone.trycloudflare.com\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nCookie: %s=%s\r\nCf-Ray: test\r\n\r\n", cookie.Name, cookie.Value)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("handshake: %+v %v", resp, err)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatal("bad RFC accept")
	}
	_, err = conn.Write(maskedFrame(1, true, []byte(`{"prompt":"phone live stream"}`)))
	if err != nil {
		t.Fatal(err)
	}
	var transcript strings.Builder
	events := 0
	for {
		op, p, err := readServerFrame(reader)
		if err != nil {
			t.Fatal(err)
		}
		if op == 8 {
			break
		}
		if op == 1 {
			events++
			transcript.Write(p)
		}
	}
	if events < 2 || !strings.Contains(transcript.String(), `"type":"done"`) || !strings.Contains(transcript.String(), `"type":"session"`) {
		t.Fatalf("events=%d %s", events, transcript.String())
	}
}
