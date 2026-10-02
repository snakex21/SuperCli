package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func hasScreenshotSchema(defs []llm.ToolDef) bool {
	for _, def := range defs {
		if def.Name == "send_screenshot" {
			return true
		}
	}
	return false
}

func TestScreenshotSchemaIsScopedToCurrentRequest(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, stable := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%v/stable=%v", thin, stable), func(t *testing.T) {
				reg := tools.NewRegistry()
				reg.MustRegister(tools.NewSendScreenshot(t.TempDir(), nil).Spec())
				loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg, ThinTools: thin, StableToolset: stable, EnableNavigator: true, NavigatorAuto: true, NavigatorKeywordsOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				for _, prompt := range []string{"cześć zrób zrzut pulpitu mi", "Take a screenshot of my desktop", "pokaż zrzut ekranu", "zrób zrzut okna Notatnik", "window capture of Notepad"} {
					loop.prepareRunRoute(context.Background(), prompt)
					if loop.route != RouteCoordinator || !hasScreenshotSchema(loop.buildToolDefs()) {
						t.Fatalf("capture schema absent for %q", prompt)
					}
					if reg.IsActive("send_screenshot") || len(reg.DiscoveredNames()) != 0 {
						t.Fatal("one-turn capability changed persistent discovery")
					}
				}
				loop.prepareRunRoute(context.Background(), "napraw plik main.go")
				if hasScreenshotSchema(loop.buildToolDefs()) {
					t.Fatal("screenshot schema leaked into unrelated next turn")
				}
				loop.SetRegistry(tools.NewRegistry())
				loop.prepareRunRoute(context.Background(), "Take a screenshot")
				if hasScreenshotSchema(loop.buildToolDefs()) {
					t.Fatal("restricted registry gained an absent capability")
				}
			})
		}
	}
}

type screenshotReplayProvider struct {
	t         *testing.T
	viaInvoke bool
	calls     int
}

func (*screenshotReplayProvider) Name() string { return "screenshot-replay" }
func (p *screenshotReplayProvider) Complete(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	if !hasScreenshotSchema(defs) {
		p.t.Fatal("real agent provider request omitted screen-capture capability")
	}
	ch := make(chan llm.Delta, 1)
	if p.calls == 1 {
		call := llm.ToolCall{ID: "capture", Name: "send_screenshot", Arguments: `{"source":"screen"}`}
		if p.viaInvoke {
			call.Name = "invoke_tool"
			call.Arguments = `{"tool":"send_screenshot","args":{"source":"screen"}}`
		}
		ch <- llm.Delta{ToolCall: &call, FinishReason: "tool_calls"}
	} else {
		for _, msg := range messages {
			if msg.HasImage() {
				p.t.Fatal("capture-only screenshot uploaded unnecessary pixels")
			}
		}
		ch <- llm.Delta{Content: "Screenshot saved.", FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}

func TestScreenshotRequestExecutesWithoutDiscoveryRound(t *testing.T) {
	for _, viaInvoke := range []bool{false, true} {
		t.Run(fmt.Sprint(viaInvoke), func(t *testing.T) {
			reg := tools.NewRegistry()
			captures := 0
			capture := tools.NewSendScreenshot(t.TempDir(), nil)
			capture.ScreenCapture = func(context.Context) ([]byte, string, error) {
				captures++
				return []byte("\x89PNG\r\n\x1a\nfixture"), "image/png", nil
			}
			reg.MustRegister(capture.Spec())
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn("invoke_tool")
			provider := &screenshotReplayProvider{t: t, viaInvoke: viaInvoke}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: true, StableToolset: true, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, loop, "cześć zrób zrzut pulpitu mi"))
			if captures != 1 || provider.calls != 2 {
				t.Fatalf("capture=%d requests=%d", captures, provider.calls)
			}
			for _, message := range loop.Messages {
				if message.Role == llm.RoleTool && message.Name == "send_screenshot" {
					var result struct {
						Path     string
						Attached bool
					}
					if err := json.Unmarshal([]byte(message.Content), &result); err != nil || result.Attached {
						t.Fatalf("preview metadata=%s error=%v", message.Content, err)
					}
					if _, err := os.Stat(result.Path); err != nil {
						t.Fatalf("saved screenshot missing: %v", err)
					}
				}
			}
		})
	}
}

func TestScreenshotRequestExposesOwnedProcessWorkflowOnlyForThatRun(t *testing.T) {
	for _, stable := range []bool{false, true} {
		reg := tools.NewRegistry()
		process := tools.NewProcessSession(t.TempDir())
		defer process.Close()
		reg.MustRegister(process.Spec())
		reg.MustRegister(tools.NewSendScreenshot(t.TempDir(), nil).Spec())
		loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg, ThinTools: true, StableToolset: stable})
		if err != nil {
			t.Fatal(err)
		}
		loop.prepareRunRoute(context.Background(), "Launch my application and take a screenshot of its window")
		found := false
		for _, def := range loop.buildToolDefs() {
			found = found || def.Name == "process_session"
		}
		if !found || reg.IsActive("process_session") {
			t.Fatal("owned launch/capture contract absent or permanently activated")
		}
		call := llm.ToolCall{ID: "shot", Name: "invoke_tool", Arguments: `{"tool":"process_session","args":{"action":"screenshot","id":"proc-1"}}`}
		if resolved := loop.resolveInvokeToolCalls([]llm.ToolCall{call}); resolved[0].Name != "process_session" {
			t.Fatal("requested owned capture required a discovery round")
		}
		loop.prepareRunRoute(context.Background(), "cześć")
		for _, def := range loop.buildToolDefs() {
			if def.Name == "process_session" {
				t.Fatal("capture workflow schema leaked into ordinary next turn")
			}
		}
		if reg.IsActive("process_session") {
			t.Fatal("run-scoped capture changed persistent discovery")
		}
	}
}
