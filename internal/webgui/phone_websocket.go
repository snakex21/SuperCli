package webgui

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const wsLimit = 4 << 20

type webSocket struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
}

func headerToken(value, token string) bool {
	for _, v := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(v), token) {
			return true
		}
	}
	return false
}
func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*webSocket, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	raw, err := base64.StdEncoding.DecodeString(key)
	if r.Method != "GET" || !headerToken(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" || err != nil || len(raw) != 16 {
		http.Error(w, "invalid WebSocket handshake", 400)
		return nil, errors.New("invalid WebSocket handshake")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "upgrade unsupported", 500)
		return nil, errors.New("upgrade unsupported")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	_, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
	if err == nil {
		err = rw.Flush()
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &webSocket{conn: conn, reader: rw.Reader}, nil
}
func (ws *webSocket) write(op byte, p []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	_ = ws.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	head := []byte{0x80 | op}
	switch {
	case len(p) < 126:
		head = append(head, byte(len(p)))
	case len(p) <= 65535:
		head = append(head, 126, byte(len(p)>>8), byte(len(p)))
	default:
		head = append(head, 127)
		head = binary.BigEndian.AppendUint64(head, uint64(len(p)))
	}
	if _, err := ws.conn.Write(head); err != nil {
		return err
	}
	_, err := ws.conn.Write(p)
	return err
}
func (ws *webSocket) fail(code uint16) error {
	_ = ws.write(8, binary.BigEndian.AppendUint16(nil, code))
	return errors.New("WebSocket protocol violation")
}
func (ws *webSocket) readText() ([]byte, error) {
	var message []byte
	fragmented := false
	for {
		_ = ws.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		var h [2]byte
		if _, err := io.ReadFull(ws.reader, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&128 != 0, h[0]&15
		if h[0]&0x70 != 0 || h[1]&128 == 0 {
			return nil, ws.fail(1002)
		}
		n := uint64(h[1] & 127)
		if n == 126 {
			var b [2]byte
			if _, err := io.ReadFull(ws.reader, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
			if n < 126 {
				return nil, ws.fail(1002)
			}
		} else if n == 127 {
			var b [8]byte
			if _, err := io.ReadFull(ws.reader, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
			if n < 65536 || n>>63 != 0 {
				return nil, ws.fail(1002)
			}
		}
		if op >= 8 && (!fin || n > 125) {
			return nil, ws.fail(1002)
		}
		if n > wsLimit || uint64(len(message))+n > wsLimit {
			return nil, ws.fail(1009)
		}
		var mask [4]byte
		if _, err := io.ReadFull(ws.reader, mask[:]); err != nil {
			return nil, err
		}
		p := make([]byte, int(n))
		if _, err := io.ReadFull(ws.reader, p); err != nil {
			return nil, err
		}
		for i := range p {
			p[i] ^= mask[i%4]
		}
		switch op {
		case 8:
			if len(p) == 1 {
				return nil, ws.fail(1002)
			}
			if len(p) >= 2 {
				code := binary.BigEndian.Uint16(p[:2])
				if code < 1000 || code >= 5000 || (code >= 1004 && code <= 1006) || (code >= 1015 && code < 3000) {
					return nil, ws.fail(1002)
				}
				if !utf8.Valid(p[2:]) {
					return nil, ws.fail(1007)
				}
			}
			_ = ws.write(8, p)
			return nil, io.EOF
		case 9:
			if err := ws.write(10, p); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 1:
			if fragmented {
				return nil, ws.fail(1002)
			}
			fragmented = true
		case 0:
			if !fragmented {
				return nil, ws.fail(1002)
			}
		default:
			return nil, ws.fail(1002)
		}
		message = append(message, p...)
		if fin {
			if !utf8.Valid(message) {
				return nil, ws.fail(1007)
			}
			return message, nil
		}
	}
}

// wsStreamWriter preserves the existing SSE event format over WebSocket frames.
// The browser feeds these frames into the same parser used on the local PC.
type wsStreamWriter struct {
	ws     *webSocket
	header http.Header
	code   int
	cancel context.CancelFunc
}

func (w *wsStreamWriter) Header() http.Header { return w.header }
func (w *wsStreamWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}
func (w *wsStreamWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.code == 0 {
		w.code = 200
	}
	if w.code != 200 {
		payload, _ := json.Marshal(map[string]string{"type": "error", "err": strings.TrimSpace(string(p))})
		p = append(append([]byte("data: "), payload...), '\n', '\n')
	}
	if err := w.ws.write(1, p); err != nil {
		w.cancel()
		return 0, err
	}
	return n, nil
}
func (w *wsStreamWriter) Flush() {}

func (s *Server) handleChatWebSocket(w http.ResponseWriter, r *http.Request) {
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		return
	}
	defer ws.conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.phone != nil && !isLocalRequest(r) {
		if active := s.phone.manager.ActiveContext(); active != nil {
			stop := context.AfterFunc(active, func() { cancel(); ws.conn.Close() })
			defer stop()
		}
	}
	payload, err := ws.readText()
	if err != nil {
		return
	}
	go func() { defer cancel(); defer ws.conn.Close(); _, _ = ws.readText() }()
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ws.write(9, nil) != nil {
					cancel()
					ws.conn.Close()
					return
				}
			}
		}
	}()
	req := r.Clone(ctx)
	req.Method = http.MethodPost
	req.Body = io.NopCloser(strings.NewReader(string(payload)))
	writer := &wsStreamWriter{ws: ws, header: make(http.Header), cancel: cancel}
	s.handleChat(writer, req)
	_ = ws.write(8, []byte{3, 232})
}
