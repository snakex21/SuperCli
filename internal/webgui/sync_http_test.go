package webgui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

type syncTestProvider struct{ entered, release, canceled chan struct{} }

func (p *syncTestProvider) Name() string { return "sync-fixture" }
func (p *syncTestProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	out := make(chan llm.Delta)
	go func() {
		defer close(out)
		select {
		case out <- llm.Delta{Content: "live prefix "}:
		case <-ctx.Done():
			close(p.canceled)
			return
		}
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			close(p.canceled)
			return
		}
		select {
		case out <- llm.Delta{Content: "finished answer"}:
		case <-ctx.Done():
			close(p.canceled)
		}
	}()
	return out, nil
}

type syncTestReader struct {
	read  func() (syncFrame, error)
	close func()
}

func openSyncSSE(t *testing.T, api *httptest.Server, session string) syncTestReader {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(api.URL + "/api/sync?session=" + session + "&client=b")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	return syncTestReader{func() (syncFrame, error) {
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") {
				var frame syncFrame
				err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &frame)
				return frame, err
			}
		}
		if err := scanner.Err(); err != nil {
			return syncFrame{}, err
		}
		return syncFrame{}, io.EOF
	}, func() { resp.Body.Close() }}
}
func dialSyncTestWS(t *testing.T, api *httptest.Server, path string, cookie *http.Cookie) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(api.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: test-phone.trycloudflare.com\r\nOrigin: https://test-phone.trycloudflare.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nCookie: %s=%s\r\nCf-Ray: test\r\n\r\n", path, cookie.Name, cookie.Value)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil || resp.StatusCode != 101 {
		conn.Close()
		t.Fatalf("handshake: %+v %v", resp, err)
	}
	return conn, reader
}
func openSyncWS(t *testing.T, api *httptest.Server, session string, cookie *http.Cookie) syncTestReader {
	conn, r := dialSyncTestWS(t, api, "/api/sync/ws?session="+session+"&client=b", cookie)
	return syncTestReader{func() (syncFrame, error) {
		for {
			op, p, err := readServerFrame(r)
			if err != nil {
				return syncFrame{}, err
			}
			if op == 8 {
				return syncFrame{}, io.EOF
			}
			if op != 1 {
				continue
			}
			var frame syncFrame
			err = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(string(p), "data: "))), &frame)
			return frame, err
		}
	}, func() { conn.Close() }}
}
func readSyncUntil(t *testing.T, r syncTestReader, kind string) syncFrame {
	t.Helper()
	for i := 0; i < 200; i++ {
		frame, err := r.read()
		if err != nil {
			t.Fatalf("waiting for %s: %v", kind, err)
		}
		if frame.Type == kind {
			return frame
		}
	}
	t.Fatalf("missing %s", kind)
	return syncFrame{}
}
func syncPost(t *testing.T, api *httptest.Server, path, body string) {
	t.Helper()
	resp, err := http.Post(api.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: %d %s", path, resp.StatusCode, data)
	}
}

func TestSyncTwoClientsDisconnectStopAndPhoneRevocation(t *testing.T) {
	for _, mode := range []string{"sse-disconnect", "ws-disconnect", "stop", "phone-stop", "phone-idle", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestServer(t, false)
			if mode == "phone-idle" {
				setupTestPhoneIdle(t, s, 2*time.Second)
			} else {
				setupTestPhone(t, s)
			}
			cookie := pairTestPhone(t, s)
			p := &syncTestProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			s.eng.prov = p
			api := httptest.NewServer(s.Handler())
			defer api.Close()
			defer s.eng.stopChatRuns()
			pc := openSyncSSE(t, api, "")
			defer pc.close()
			readSyncUntil(t, pc, "snapshot")
			phone := openSyncWS(t, api, "", cookie)
			defer phone.close()
			readSyncUntil(t, phone, "snapshot")
			payload := `{"prompt":"hello sync","turn_id":"sync-turn","client_id":"a"}`
			var disconnect func()
			if mode == "ws-disconnect" || strings.HasPrefix(mode, "phone-") {
				conn, _ := dialSyncTestWS(t, api, "/api/chat/ws", cookie)
				if _, err := conn.Write(maskedFrame(1, true, []byte(payload))); err != nil {
					t.Fatal(err)
				}
				disconnect = func() { conn.Close() }
			} else {
				resp, err := http.Post(api.URL+"/api/chat", "application/json", strings.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				disconnect = func() { resp.Body.Close() }
			}
			defer disconnect()
			start := readSyncUntil(t, pc, "run_started")
			if start.Prompt != "hello sync" || start.ClientID != "a" {
				t.Fatal(start)
			}
			readSyncUntil(t, pc, "sessions_changed")
			readSyncUntil(t, phone, "run_started")
			readSyncUntil(t, phone, "sessions_changed")
			waitCompletionTest(t, p.entered, "provider")
			pc.close()
			phone.close()
			pc = openSyncSSE(t, api, start.SessionID)
			defer pc.close()
			snapshot := readSyncUntil(t, pc, "snapshot")
			phone = openSyncWS(t, api, start.SessionID, cookie)
			defer phone.close()
			readSyncUntil(t, phone, "snapshot")
			accepted := false
			for _, ev := range snapshot.Events {
				if ev.Type == "session_activity" {
					accepted = ev.UserSeq > 0
				}
			}
			if !accepted {
				t.Fatalf("no persisted user receipt in replay: %+v", snapshot)
			}
			switch mode {
			case "stop":
				syncPost(t, api, "/api/sync/stop", fmt.Sprintf(`{"session_id":%q,"turn_id":"sync-turn"}`, start.SessionID))
				waitCompletionTest(t, p.canceled, "Stop")
			case "shutdown":
				api.Config.RegisterOnShutdown(s.eng.stopChatRuns)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := api.Config.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
				waitCompletionTest(t, p.canceled, "shutdown")
				s.eng.chatLifetime.wg.Wait()
				if s.eng.sessionRunActive(start.SessionID) {
					t.Fatal("gate retained after shutdown")
				}
				return
			case "phone-stop", "phone-idle":
				reason := strings.TrimPrefix(mode, "phone-")
				syncExpiry := s.phone.manager.Status().ExpiresAt
				if reason == "idle" {
					select {
					case <-s.phone.manager.ActiveContext().Done():
					case <-time.After(4 * time.Second):
						t.Fatal("tunnel did not expire")
					}
					if s.phone.manager.Status().Reason != "idle" {
						t.Fatal(s.phone.manager.Status())
					}
				} else {
					s.phone.manager.Stop(reason)
				}
				if _, err := phone.read(); err == nil {
					t.Fatal("revoked phone sync survived")
				}
				if s.phone.manager.Status().ExpiresAt != syncExpiry {
					t.Fatal("sync renewed idle")
				}
			default:
				disconnect()
			}
			if mode != "stop" {
				select {
				case <-p.canceled:
					t.Fatal("disconnect canceled generation")
				default:
				}
				close(p.release)
			}
			events := []wireEvent{}
			for {
				frame, err := pc.read()
				if err != nil {
					t.Fatal(err)
				}
				if frame.Event != nil {
					events = append(events, *frame.Event)
				}
				if frame.Type == "run_finished" {
					break
				}
			}
			readSyncUntil(t, pc, "sessions_changed")
			if !strings.HasPrefix(mode, "phone-") {
				readSyncUntil(t, phone, "run_finished")
				readSyncUntil(t, phone, "sessions_changed")
			}
			if s.eng.sessionRunActive(start.SessionID) {
				t.Fatal("session gate not released before finish")
			}
			if mode != "stop" {
				live, done := false, false
				for _, ev := range events {
					live = live || ev.Type == "message"
					done = done || ev.Type == "done"
				}
				if !live || !done {
					t.Fatalf("no live stream/terminal: %+v", events)
				}
				store, err := s.eng.sessionStore()
				if err != nil {
					t.Fatal(err)
				}
				msgs, err := store.ReadMessages(context.Background(), start.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				text := ""
				for _, m := range msgs {
					if m.Role == "assistant" {
						text += webCapsuleText(m)
					}
				}
				if !strings.Contains(text, "finished answer") {
					t.Fatalf("not durable: %+v", msgs)
				}
			}
			replay := openSyncSSE(t, api, start.SessionID)
			defer replay.close()
			if len(readSyncUntil(t, replay, "snapshot").Runs) != 0 {
				t.Fatal("finished run in snapshot")
			}
		})
	}
}
