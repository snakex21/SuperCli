package testbrowser

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func serverFrame(fin bool, op byte, payload []byte) []byte {
	head := op
	if fin {
		head |= 128
	}
	frame := []byte{head}
	switch {
	case len(payload) < 126:
		frame = append(frame, byte(len(payload)))
	case len(payload) <= 65535:
		frame = append(frame, 126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	default:
		frame = append(frame, 127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(len(payload)))
	}
	return append(frame, payload...)
}

func readClientFrame(reader io.Reader) (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		return 0, nil, err
	}
	if head[0]&0xf0 != 0x80 || head[1]&128 == 0 {
		return 0, nil, errors.New("client frame must be final and masked")
	}
	n := uint64(head[1] & 127)
	if n == 126 {
		var size [2]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(size[:]))
		if n < 126 {
			return 0, nil, errors.New("non-minimal client 16-bit length")
		}
	} else if n == 127 {
		var size [8]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(size[:])
		if n < 65536 {
			return 0, nil, errors.New("non-minimal client 64-bit length")
		}
	}
	if n > maxWSMessage {
		return 0, nil, errors.New("oversized client frame")
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, int(n))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return head[0] & 15, payload, nil
}

func socketPair(t *testing.T) (*webSocket, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { client.Close(); server.Close() })
	return &webSocket{conn: client, reader: bufio.NewReader(client)}, server
}

func TestWebSocketMaskedLengths(t *testing.T) {
	for _, length := range []int{0, 1, 125, 126, 65535, 65536} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			ws, server := socketPair(t)
			payload := bytes.Repeat([]byte("x"), length)
			done := make(chan error, 1)
			go func() {
				op, received, err := readClientFrame(server)
				if err == nil && (op != 1 || !bytes.Equal(received, payload)) {
					err = errors.New("incorrect masked payload")
				}
				if err == nil {
					_, err = server.Write(serverFrame(true, 1, received))
				}
				done <- err
			}()
			if err := ws.writeFrame(t.Context(), 1, payload); err != nil {
				t.Fatal(err)
			}
			got, err := ws.readText()
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("roundtrip length=%d: %v", len(got), err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWebSocketFragmentPingPongClose(t *testing.T) {
	ws, server := socketPair(t)
	done := make(chan error, 1)
	go func() {
		var err error
		defer func() { done <- err }()
		// Split a UTF-8 code point across frames. Ping/pong must not reset it.
		_, err = server.Write(serverFrame(false, 1, []byte{'A', 0xe2}))
		if err != nil {
			return
		}
		_, err = server.Write(serverFrame(true, 9, []byte("probe")))
		if err != nil {
			return
		}
		var op byte
		var payload []byte
		op, payload, err = readClientFrame(server)
		if err != nil {
			return
		}
		if op != 10 || string(payload) != "probe" {
			err = errors.New("missing masked pong")
			return
		}
		_, err = server.Write(serverFrame(true, 10, nil))
		if err != nil {
			return
		}
		_, err = server.Write(serverFrame(true, 0, []byte{0x82, 0xac, 'Z'}))
		if err != nil {
			return
		}
		closePayload := append(binary.BigEndian.AppendUint16(nil, 1000), []byte("done")...)
		_, err = server.Write(serverFrame(true, 8, closePayload))
		if err != nil {
			return
		}
		op, payload, err = readClientFrame(server)
		if err == nil && (op != 8 || !bytes.Equal(payload, closePayload)) {
			err = errors.New("missing masked close echo")
		}
	}()
	got, err := ws.readText()
	if err != nil || string(got) != "A€Z" {
		t.Fatalf("fragmented text %q: %v", got, err)
	}
	if _, err := ws.readText(); !errors.Is(err, io.EOF) {
		t.Fatalf("close: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWebSocketRejectsInvalidFrames(t *testing.T) {
	oversize := binary.BigEndian.AppendUint64([]byte{0x81, 127}, maxWSMessage+1)
	tests := []struct {
		name string
		wire []byte
		code uint16
	}{
		{"RSV", []byte{0xc1, 0}, 1002},
		{"masked server", []byte{0x81, 0x80}, 1002},
		{"unexpected continuation", []byte{0x80, 0}, 1002},
		{"binary", []byte{0x82, 0}, 1002},
		{"interleaved text", []byte{0x01, 0, 0x81, 0}, 1002},
		{"16-bit nonminimal", []byte{0x81, 126, 0, 1}, 1002},
		{"64-bit nonminimal", binary.BigEndian.AppendUint64([]byte{0x81, 127}, 126), 1002},
		{"64-bit high bit", binary.BigEndian.AppendUint64([]byte{0x81, 127}, 1<<63), 1002},
		{"bounded message", oversize, 1009},
		{"fragmented ping", []byte{0x09, 0}, 1002},
		{"large ping", []byte{0x89, 126, 0, 126}, 1002},
		{"invalid UTF8", []byte{0x81, 1, 0xff}, 1007},
		{"short close", []byte{0x88, 1, 1}, 1002},
		{"reserved close status", serverFrame(true, 8, binary.BigEndian.AppendUint16(nil, 1005)), 1002},
		{"invalid close UTF8", serverFrame(true, 8, []byte{3, 232, 255}), 1007},
		{"unknown opcode", []byte{0x8b, 0}, 1002},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ws, server := socketPair(t)
			done := make(chan error, 1)
			go func() {
				_, err := server.Write(test.wire)
				if err == nil {
					op, payload, readErr := readClientFrame(server)
					err = readErr
					if err == nil && (op != 8 || len(payload) != 2 || binary.BigEndian.Uint16(payload) != test.code) {
						err = fmt.Errorf("bad protocol close: opcode=%d payload=%v", op, payload)
					}
				}
				done <- err
			}()
			if _, err := ws.readText(); err == nil || !strings.Contains(err.Error(), "protocol error") {
				t.Fatalf("expected protocol error, got %v", err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWebSocketOutgoingBounds(t *testing.T) {
	ws, _ := socketPair(t)
	if err := ws.writeFrame(t.Context(), 1, make([]byte, maxWSMessage+1)); err == nil {
		t.Fatal("accepted oversized data")
	}
	if err := ws.writeFrame(t.Context(), 9, make([]byte, 126)); err == nil {
		t.Fatal("accepted oversized control")
	}
}

func TestLoopbackEndpointsOnly(t *testing.T) {
	for _, endpoint := range []string{"ws://127.0.0.1:123/a", "ws://[::1]:123/a", "ws://localhost:123/a"} {
		u, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loopbackAddress(u); err != nil {
			t.Errorf("%s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"ws://example.com:123/a", "ws://192.168.1.1:123/a", "ws://0.0.0.0:123/a", "ws://127.0.0.1/a", "ws://127.0.0.1:0/a", "ws://127.0.0.1:65536/a", "wss://127.0.0.1:123/a", "ws://user@127.0.0.1:123/a", "ws://127.0.0.1:123/a#x", "ws://localhost.evil:123/a"} {
		if ws, err := dialWebSocket(t.Context(), endpoint); err == nil {
			ws.conn.Close()
			t.Errorf("accepted %s", endpoint)
		}
	}
}

func TestWebSocketHandshake(t *testing.T) {
	for _, variant := range []string{"valid", "bad accept", "duplicate accept", "missing upgrade", "missing connection", "extension", "protocol", "bad status", "large headers"} {
		t.Run(variant, func(t *testing.T) {
			done := make(chan struct{})
			t.Cleanup(func() { close(done) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.Header.Get("Sec-WebSocket-Key")
				nonce, err := base64.StdEncoding.DecodeString(key)
				if err != nil || len(nonce) != 16 || r.Header.Get("Sec-WebSocket-Version") != "13" || r.URL.RequestURI() != "/devtools/browser/test?x=1" {
					t.Error("bad client handshake")
				}
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				sum := sha1.Sum([]byte(key + webSocketGUID))
				accept := base64.StdEncoding.EncodeToString(sum[:])
				status, upgrade, connection, extra := "101 Switching Protocols", "websocket", "keep-alive, Upgrade", ""
				switch variant {
				case "bad accept":
					accept = "not-the-server-accept"
				case "duplicate accept":
					extra = "Sec-WebSocket-Accept: " + accept + "\r\n"
				case "missing upgrade":
					upgrade = "h2c"
				case "missing connection":
					connection = "keep-alive"
				case "extension":
					extra = "Sec-WebSocket-Extensions: permessage-deflate\r\n"
				case "protocol":
					extra = "Sec-WebSocket-Protocol: unwanted\r\n"
				case "bad status":
					status = "200 OK"
				case "large headers":
					extra = strings.Repeat("X-Large: "+strings.Repeat("x", 1000)+"\r\n", 20)
				}
				_, _ = fmt.Fprintf(rw, "HTTP/1.1 %s\r\nUpgrade: %s\r\nConnection: %s\r\nSec-WebSocket-Accept: %s\r\n%s\r\n", status, upgrade, connection, accept, extra)
				_, _ = rw.Write(serverFrame(true, 1, []byte("buffered after headers")))
				_ = rw.Flush()
				<-done
			}))
			t.Cleanup(server.Close)
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test?x=1"
			ws, err := dialWebSocket(t.Context(), endpoint)
			if variant != "valid" {
				if err == nil {
					ws.conn.Close()
					t.Fatal("accepted invalid upgrade")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer ws.conn.Close()
			got, err := ws.readText()
			if err != nil || string(got) != "buffered after headers" {
				t.Fatalf("lost buffered frame: %q %v", got, err)
			}
		})
	}
}

func TestWebSocketHandshakeCancellation(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		<-done
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context()) // deliberately no deadline
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	if ws, err := dialWebSocket(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/"); err == nil {
		ws.conn.Close()
		t.Fatal("handshake ignored cancellation")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("handshake cancellation was not prompt")
	}
}
