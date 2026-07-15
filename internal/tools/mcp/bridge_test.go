package mcp

import (
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestBridgeListsWithoutStartingAndCanSearchAndCall(t *testing.T) {
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	go fakeServer(t, serverIn, serverOut)
	t.Cleanup(func() { _ = clientOut.Close(); _ = serverOut.Close() })
	client := NewClient(clientIn, clientOut)
	ctx := testCtx(t)
	if _, err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	toolDefs, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		Name: "portable-demo", Config: ServerConfig{Description: "portable demo", Portable: true, Tags: []string{"demo"}},
		cmd: &exec.Cmd{}, client: client, tools: toolDefs,
	}
	manager := &Manager{servers: map[string]*Server{"portable-demo": server}}
	bridge := NewBridge(manager)

	list, err := bridge.Spec().Fn(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil || list.Err != nil || !strings.Contains(list.Text, `"portable":true`) {
		t.Fatalf("list = %+v, err=%v", list, err)
	}
	search, _ := bridge.Spec().Fn(ctx, json.RawMessage(`{"action":"search","server":"portable-demo","query":"echo"}`))
	if search.Err != nil || !strings.Contains(search.Text, `"name":"echo"`) {
		t.Fatalf("search = %+v", search)
	}
	call, _ := bridge.Spec().Fn(ctx, json.RawMessage(`{"action":"call","server":"portable-demo","tool":"echo","arguments":{"text":"hi"}}`))
	if call.Err != nil || !strings.Contains(call.Text, "echo:") {
		t.Fatalf("call = %+v", call)
	}

	// Smaller/local models often encode the nested MCP arguments object as a
	// JSON string. The bridge repairs that form instead of silently forwarding
	// a string that makes every required remote parameter look absent.
	stringCall, _ := bridge.Spec().Fn(ctx, json.RawMessage(`{"action":"call","server":"portable-demo","tool":"echo","arguments":"{\"text\":\"from-string\"}"}`))
	if stringCall.Err != nil || !strings.Contains(stringCall.Text, `{"text":"from-string"}`) {
		t.Fatalf("stringified arguments call = %+v", stringCall)
	}
}

func TestNormalizeBridgeArgumentsRejectsNonObjects(t *testing.T) {
	for _, raw := range []string{`[]`, `"plain text"`, `42`} {
		if _, err := normalizeBridgeArguments(json.RawMessage(raw)); err == nil {
			t.Fatalf("normalizeBridgeArguments(%s) succeeded, want error", raw)
		}
	}
}
