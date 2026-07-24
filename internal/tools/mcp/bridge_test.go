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

func TestDecodeBridgeRequestAcceptsToolAsActionAlias(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		wantAction string
		wantTool   string
	}{
		{name: "search", raw: `{"tool":"search","query":"browser"}`, wantAction: "search"},
		{name: "list case and whitespace", raw: `{"tool":" LIST "}`, wantAction: "list"},
		{name: "canonical call wins", raw: `{"action":"call","tool":"search"}`, wantAction: "call", wantTool: "search"},
		{name: "call is not an ambiguous alias", raw: `{"tool":"call"}`, wantTool: "call"},
		{name: "unknown remains invalid", raw: `{"tool":"remote_search"}`, wantTool: "remote_search"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := decodeBridgeRequest(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if req.Action != tc.wantAction || req.Tool != tc.wantTool {
				t.Fatalf("request = %+v, want action=%q tool=%q", req, tc.wantAction, tc.wantTool)
			}
		})
	}
}

func TestBridgeToolSearchAliasUsesSearchAction(t *testing.T) {
	manager := &Manager{servers: map[string]*Server{}}
	bridge := NewBridge(manager)
	result, err := bridge.Spec().Fn(testCtx(t), json.RawMessage(`{"tool":"search","query":"browser"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("alias search result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Text, "no MCP server metadata matched") {
		t.Fatalf("alias did not route to search: %+v", result)
	}
}
