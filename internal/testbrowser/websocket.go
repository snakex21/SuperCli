// Package testbrowser provides a small, development-only headless CDP driver.
// It is not a general browser automation or production WebSocket library.
package testbrowser

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	webSocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxWSMessage  = 32 << 20
	maxWSHeaders  = 16 << 10
)

type webSocket struct {
	conn   net.Conn
	reader *bufio.Reader
	write  sync.Mutex
}

// loopbackAddress deliberately does not use DNS, proxies, or redirects. Even an
// endpoint printed by a browser process must not turn into a remote CDP client.
func loopbackAddress(u *url.URL) (string, error) {
	if u.Scheme != "ws" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("CDP endpoint must be a loopback ws:// URL")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return "", errors.New("CDP endpoint must have a loopback address and an explicit port")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func dialWebSocket(ctx context.Context, endpoint string) (_ *webSocket, err error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	address, err := loopbackAddress(u)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	release := connectionContext(ctx, conn, false)
	defer release()
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	req := &http.Request{Method: http.MethodGet, URL: u, Host: u.Host, Header: make(http.Header)}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Version", "13")
	if err = req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	var headers strings.Builder
	for {
		line, readErr := reader.ReadSlice('\n')
		if readErr != nil {
			return nil, fmt.Errorf("WebSocket handshake headers: %w", readErr)
		}
		if headers.Len()+len(line) > maxWSHeaders {
			return nil, errors.New("WebSocket handshake headers exceed limit")
		}
		headers.Write(line)
		if string(line) == "\r\n" {
			break
		}
	}
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(headers.String())), req)
	if err != nil {
		return nil, fmt.Errorf("WebSocket handshake: %w", err)
	}
	sum := sha1.Sum([]byte(key + webSocketGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	if resp.StatusCode != http.StatusSwitchingProtocols ||
		!strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") ||
		!headerToken(resp.Header.Get("Connection"), "upgrade") ||
		len(resp.Header.Values("Sec-WebSocket-Accept")) != 1 ||
		resp.Header.Get("Sec-WebSocket-Accept") != accept ||
		resp.Header.Get("Sec-WebSocket-Extensions") != "" ||
		resp.Header.Get("Sec-WebSocket-Protocol") != "" {
		return nil, fmt.Errorf("invalid WebSocket upgrade/accept (HTTP %d)", resp.StatusCode)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return &webSocket{conn: conn, reader: reader}, nil
}

func headerToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// Interrupt blocked I/O on cancellation, including contexts without deadlines.
// Waiting for the callback before resetting the deadline avoids a cancellation
// racing with the next command on this connection.
func connectionContext(ctx context.Context, conn net.Conn, writeOnly bool) func() {
	set := conn.SetDeadline
	if writeOnly {
		set = conn.SetWriteDeadline
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = set(deadline)
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = set(time.Now())
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
		_ = set(time.Time{})
	}
}

func (ws *webSocket) writeFrame(ctx context.Context, op byte, payload []byte) error {
	if len(payload) > maxWSMessage || (op >= 8 && len(payload) > 125) {
		return errors.New("WebSocket outgoing frame exceeds limit")
	}
	ws.write.Lock()
	defer ws.write.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	release := connectionContext(ctx, ws.conn, true)
	defer release()
	frame := make([]byte, 0, len(payload)+14)
	frame = append(frame, 0x80|op)
	switch {
	case len(payload) < 126:
		frame = append(frame, 0x80|byte(len(payload)))
	case len(payload) <= 65535:
		frame = append(frame, 0x80|126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(len(payload)))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	for len(frame) > 0 {
		n, err := ws.conn.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func (ws *webSocket) control(op byte, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return ws.writeFrame(ctx, op, payload)
}

func (ws *webSocket) protocolError(code uint16, detail string) error {
	_ = ws.control(8, binary.BigEndian.AppendUint16(nil, code))
	return fmt.Errorf("WebSocket protocol error: %s", detail)
}

func (ws *webSocket) readText() ([]byte, error) {
	var message []byte
	fragmented := false
	for {
		var head [2]byte
		if _, err := io.ReadFull(ws.reader, head[:]); err != nil {
			return nil, err
		}
		fin, op := head[0]&0x80 != 0, head[0]&15
		if head[0]&0x70 != 0 || head[1]&0x80 != 0 {
			return nil, ws.protocolError(1002, "RSV bits or masked server frame")
		}
		n := uint64(head[1] & 127)
		switch n {
		case 126:
			var size [2]byte
			if _, err := io.ReadFull(ws.reader, size[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(size[:]))
			if n < 126 {
				return nil, ws.protocolError(1002, "non-minimal 16-bit length")
			}
		case 127:
			var size [8]byte
			if _, err := io.ReadFull(ws.reader, size[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(size[:])
			if n < 65536 || n>>63 != 0 {
				return nil, ws.protocolError(1002, "invalid 64-bit length")
			}
		}
		if op >= 8 && (!fin || n > 125) {
			return nil, ws.protocolError(1002, "fragmented or oversized control frame")
		}
		if n > maxWSMessage || (op < 8 && uint64(len(message))+n > maxWSMessage) {
			return nil, ws.protocolError(1009, "message exceeds limit")
		}
		payload := make([]byte, int(n))
		if _, err := io.ReadFull(ws.reader, payload); err != nil {
			return nil, err
		}
		switch op {
		case 8:
			if len(payload) == 1 {
				return nil, ws.protocolError(1002, "invalid close payload")
			}
			if len(payload) >= 2 {
				code := binary.BigEndian.Uint16(payload[:2])
				if code < 1000 || code >= 5000 || (code >= 1004 && code <= 1006) || (code >= 1015 && code < 3000) {
					return nil, ws.protocolError(1002, "invalid close status")
				}
				if !utf8.Valid(payload[2:]) {
					return nil, ws.protocolError(1007, "invalid close UTF-8")
				}
			}
			_ = ws.control(8, payload)
			return nil, io.EOF
		case 9:
			if err := ws.control(10, payload); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 1:
			if fragmented {
				return nil, ws.protocolError(1002, "interleaved data messages")
			}
			fragmented = true
		case 0:
			if !fragmented {
				return nil, ws.protocolError(1002, "unexpected continuation")
			}
		default:
			return nil, ws.protocolError(1002, "unsupported opcode")
		}
		message = append(message, payload...)
		if fin {
			if !utf8.Valid(message) {
				return nil, ws.protocolError(1007, "invalid text UTF-8")
			}
			return message, nil
		}
	}
}
