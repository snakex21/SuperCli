package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
)

func TestQMPTypeSendsEachKeyWithShift(t *testing.T) {
	type ev struct {
		key  string
		down bool
	}
	received := make(chan []ev, 16)
	endpoint := qmpFixture(t, func(request map[string]json.RawMessage, conn net.Conn) {
		var command string
		_ = json.Unmarshal(request["execute"], &command)
		if command != "input-send-event" {
			t.Error(command)
		}
		var args struct {
			Events []struct {
				Data struct {
					Down bool
					Key  struct{ Data string }
				}
			}
		}
		_ = json.Unmarshal(request["arguments"], &args)
		batch := []ev{}
		for _, e := range args.Events {
			batch = append(batch, ev{e.Data.Key.Data, e.Data.Down})
		}
		received <- batch
		fmt.Fprintf(conn, "{\"return\":{},\"id\":%s}\n", request["id"])
	})
	result := executeQMP(t, New(t.TempDir(), t.TempDir()), endpoint, "type", map[string]any{"text": "aB:\n", "delay_ms": 1})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	want := [][]ev{
		{{"a", true}}, {{"a", false}},
		{{"shift", true}, {"b", true}}, {{"b", false}, {"shift", false}},
		{{"shift", true}, {"semicolon", true}}, {{"semicolon", false}, {"shift", false}},
		{{"ret", true}}, {{"ret", false}},
	}
	for i, w := range want {
		got := <-received
		if fmt.Sprint(got) != fmt.Sprint(w) {
			t.Fatalf("batch %d = %v, want %v", i, got, w)
		}
	}
}

func TestQMPTypePolishLayoutUsesAltGr(t *testing.T) {
	strokes, err := qmpTypeStrokes("żŁa", "pl")
	if err != nil {
		t.Fatal(err)
	}
	want := []qmpStroke{{key: "z", altgr: true}, {key: "l", altgr: true, shift: true}, {key: "a"}}
	if fmt.Sprint(strokes) != fmt.Sprint(want) {
		t.Fatalf("%v want %v", strokes, want)
	}
	if _, err := qmpTypeStrokes("ż", "us"); err == nil {
		t.Fatal("us layout must reject Polish letters")
	}
	if _, err := qmpTypeStrokes("a", "de"); err == nil {
		t.Fatal("unknown layout accepted")
	}
}

func TestQMPScreenshotAfterKeysUsesSameCall(t *testing.T) {
	pixels := fixturePNG(t)
	commands := make(chan string, 4)
	endpoint := qmpFixture(t, func(request map[string]json.RawMessage, conn net.Conn) {
		var command string
		_ = json.Unmarshal(request["execute"], &command)
		commands <- command
		if command == "screendump" {
			var args struct{ Filename string }
			_ = json.Unmarshal(request["arguments"], &args)
			if err := os.WriteFile(args.Filename, pixels, 0600); err != nil {
				t.Error(err)
			}
		}
		fmt.Fprintf(conn, "{\"return\":{},\"id\":%s}\n", request["id"])
	})
	result := executeQMP(t, New(t.TempDir(), t.TempDir()), endpoint, "keys",
		map[string]any{"keys": []string{"ret"}, "screenshot_after": true, "settle_ms": 1})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if a, b := <-commands, <-commands; a != "input-send-event" || b != "screendump" {
		t.Fatal(a, b)
	}
	var meta struct {
		PreviewPath string `json:"preview_path"`
		OK          bool   `json:"ok"`
	}
	if err := json.Unmarshal([]byte(result.Text), &meta); err != nil || !meta.OK || !strings.HasPrefix(meta.PreviewPath, "snapshot:") ||
		!strings.Contains(result.Text, `"action":"keys"`) {
		t.Fatalf("metadata %s %v", result.Text, err)
	}
}

func TestQMPTypeValidationBeforeConnecting(t *testing.T) {
	tool := New(t.TempDir(), t.TempDir())
	for _, raw := range []string{
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"type","text":"zażółć"}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"type","text":"€","layout":"pl"}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"type","text":""}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"status","screenshot_after":true}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"type","text":"x","delay_ms":5000}`,
		`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"type","text":"` + strings.Repeat("a", 2000) + `","delay_ms":1000}`,
	} {
		ctx := fixtureApproval(t, context.Background(), tool, "qmp", "tcp://127.0.0.1:4444")
		r, _ := tool.Execute(ctx, []byte(raw))
		if r.Err == nil || strings.Contains(r.Err.Error(), "connect QMP") {
			t.Fatalf("%.80s: unexpected %v", raw, r.Err)
		}
	}
}

func TestQMPTypeAuthorizedByKeysGrant(t *testing.T) {
	type_ := params{Protocol: "qmp", Action: "type"}
	if !allowsAction([]string{"keys"}, type_) || allowsAction([]string{"click"}, type_) {
		t.Fatal("type must follow the keys grant")
	}
	shot := params{Protocol: "qmp", Action: "keys", ScreenshotAfter: true}
	if allowsAction([]string{"keys"}, shot) || !allowsAction([]string{"keys", "screenshot"}, shot) {
		t.Fatal("screenshot_after must also need the screenshot grant")
	}
	if allowsAction([]string{"keys"}, params{Protocol: "webdriver", Action: "type"}) {
		t.Fatal("webdriver type must not inherit keys")
	}
}
