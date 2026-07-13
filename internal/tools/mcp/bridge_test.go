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
}
