package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestDecodeToolResultResourceLinkAndEmbeddedResources(t *testing.T) {
	pdf := []byte("%PDF-1.4 fixture")
	raw, _ := json.Marshal(map[string]any{"content": []map[string]any{
		{"type": "resource_link", "uri": "file:///tmp/report.pdf", "name": "report", "mimeType": "application/pdf"},
		{"type": "resource", "resource": map[string]any{"uri": "mem://notes", "mimeType": "text/plain", "text": "hello notes"}},
		{"type": "resource", "resource": map[string]any{"uri": "mem://shot", "mimeType": "image/png", "blob": base64.StdEncoding.EncodeToString(mcpPNG)}},
		{"type": "resource", "resource": map[string]any{"uri": "mem://doc", "mimeType": "application/pdf", "blob": base64.StdEncoding.EncodeToString(pdf)}},
		{"type": "resource", "resource": map[string]any{"uri": "mem://bad", "blob": "!!!"}},
	}})
	res, err := decodeToolResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[resource link: report <file:///tmp/report.pdf> application/pdf]", "[resource mem://notes]\nhello notes", "mem://bad omitted: invalid base64"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("text %q missing %q", res.Text, want)
		}
	}
	if len(res.Images) != 1 || res.Images[0].Name != "mem://shot" || res.Images[0].MediaType != "image/png" {
		t.Fatalf("images = %+v", res.Images)
	}
	if len(res.Files) != 1 || res.Files[0].MediaType != "application/pdf" || string(res.Files[0].Data) != string(pdf) {
		t.Fatalf("files = %+v", res.Files)
	}
}

func TestResolveImageModePrecedence(t *testing.T) {
	for _, tc := range []struct{ server, hint, global, want string }{
		{"", "", "", ImageModeModel},
		{"", "", "file", ImageModeFile},
		{"", "preview", "file", ImageModePreview},
		{"file", "preview", "model", ImageModeFile},
		{"bogus", "PREVIEW", "", ImageModePreview},
	} {
		if got := ResolveImageMode(tc.server, tc.hint, tc.global); got != tc.want {
			t.Fatalf("%+v: got %q", tc, got)
		}
	}
	s := &Server{Config: ServerConfig{}, tools: []ToolDef{{Name: "shot", Meta: json.RawMessage(`{"supercli/image_mode":"preview","other":1}`)}}}
	if got := s.imageModeFor("shot", "file"); got != ImageModePreview {
		t.Fatalf("tool hint ignored: %q", got)
	}
	s.Config.ImageMode = "file"
	if got := s.imageModeFor("shot", "model"); got != ImageModeFile {
		t.Fatalf("server override ignored: %q", got)
	}
}

func TestShapeResultModesAndLargeText(t *testing.T) {
	img := &core.ImageContent{MediaType: "image/png", Data: mcpPNG}
	file := &core.ImageContent{MediaType: "application/pdf", Data: []byte("%PDF")}
	in := Result{Text: "ok", Images: []*core.ImageContent{img}, Files: []*core.ImageContent{file}}
	model := shapeResult(in, ImageModeModel, 100, 20)
	if len(model.Images) != 1 || !model.MediaNote || len(model.Files) != 1 || len(model.PreviewImages) != 0 {
		t.Fatalf("model = %+v", model)
	}
	preview := shapeResult(in, ImageModePreview, 100, 20)
	if len(preview.Images) != 0 || len(preview.PreviewImages) != 1 || len(preview.Files) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	asFile := shapeResult(in, ImageModeFile, 100, 20)
	if len(asFile.Images) != 0 || len(asFile.PreviewImages) != 0 || len(asFile.Files) != 2 {
		t.Fatalf("file = %+v", asFile)
	}

	big := strings.Repeat("line of output\n", 4000)
	large := shapeResult(Result{Text: big}, "", bridgeResultHead, bridgeResultTail)
	if large.RetainedText != big || len(large.Text) >= len(big) {
		t.Fatalf("large text not retained: text=%d retained=%d", len(large.Text), len(large.RetainedText))
	}
	reg := core.NewRegistry()
	content := reg.ModelResultContentContext(context.Background(), "mcp_bridge", large)
	handle := core.StoredOutputHandle(content)
	if handle == "" || !strings.Contains(content, "read_output") {
		t.Fatalf("no read_output handle in %q", content[len(content)-300:])
	}
	small := shapeResult(Result{Text: "tiny"}, "", bridgeResultHead, bridgeResultTail)
	if small.RetainedText != "" {
		t.Fatal("small text retained")
	}
}

func TestBridgeBatchCallsSequentialWithPerItemErrors(t *testing.T) {
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
	server := &Server{Name: "demo", cmd: &exec.Cmd{}, client: client, tools: toolDefs}
	bridge := NewBridge(&Manager{servers: map[string]*Server{"demo": server}})
	run := func(raw string) core.Result {
		res, err := bridge.Spec().Fn(ctx, json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := run(`{"action":"call","calls":[{"server":"demo","tool":"echo","arguments":{"text":"one"}},{"server":"demo","tool":"fail"},{"server":"nope","tool":"echo"},{"server":"demo","tool":"echo","arguments":"{\"text\":\"two\"}"}]}`)
	if res.Err != nil {
		t.Fatalf("batch err: %v", res.Err)
	}
	one, fail, unknown, two := strings.Index(res.Text, `"one"`), strings.Index(res.Text, "boom"), strings.Index(res.Text, `unknown MCP server "nope"`), strings.Index(res.Text, `"two"`)
	if one < 0 || fail < 0 || unknown < 0 || two < 0 || !(one < fail && fail < unknown && unknown < two) {
		t.Fatalf("sections missing or out of order:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "## [1/4] demo/echo") || !strings.Contains(res.Text, "## [4/4] demo/echo") {
		t.Fatalf("headers: %s", res.Text)
	}
	if all := run(`{"action":"call","calls":[{"server":"demo","tool":"fail"}]}`); all.Err == nil {
		t.Fatal("all-failed batch should report an error")
	}
	tooMany := `{"action":"call","calls":[` + strings.TrimSuffix(strings.Repeat(`{"server":"demo","tool":"echo"},`, 9), ",") + `]}`
	if res := run(tooMany); res.Err == nil || !strings.Contains(res.Err.Error(), "at most 8") {
		t.Fatalf("limit not enforced: %+v", res)
	}
}
