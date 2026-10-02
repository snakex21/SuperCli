package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

func hasHeadlessSchema(defs []llm.ToolDef) bool {
	for _, d := range defs {
		if d.Name == "headless_control" {
			return true
		}
	}
	return false
}
func TestHeadlessSchemasAreRequestScopedAndRestricted(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, stable := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%v/stable=%v", thin, stable), func(t *testing.T) {
				reg := tools.NewRegistry()
				reg.MustRegister(tools.NewHeadlessControl(t.TempDir(), t.TempDir()).Spec())
				proc := tools.NewProcessSession(t.TempDir())
				defer proc.Close()
				reg.MustRegister(proc.Spec())
				loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg, ThinTools: thin, StableToolset: stable, EnableNavigator: true, NavigatorAuto: true, NavigatorKeywordsOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				loop.prepareRunRoute(context.Background(), "cześć")
				baseline, _ := json.Marshal(loop.buildToolDefs())
				for _, prompt := range []string{"Control a headless browser through WebDriver", "zrób zrzut maszyny wirtualnej QEMU", "QMP keys ctrl alt delete"} {
					loop.prepareRunRoute(context.Background(), prompt)
					defs := loop.buildToolDefs()
					if loop.route != RouteCoordinator || !hasHeadlessSchema(defs) {
						t.Fatalf("missing %q", prompt)
					}
					process := false
					for _, def := range defs {
						process = process || def.Name == "process_session"
					}
					if !process {
						t.Fatal("missing reusable launcher")
					}
					if reg.IsActive("headless_control") || reg.IsActive("process_session") || len(reg.DiscoveredNames()) != 0 {
						t.Fatal("persistent discovery changed")
					}
				}
				loop.prepareRunRoute(context.Background(), "cześć")
				after, _ := json.Marshal(loop.buildToolDefs())
				if string(after) != string(baseline) {
					t.Fatalf("ordinary request acquired headless tokens %s", after)
				}
				loop.SetRegistry(tools.NewRegistry())
				loop.prepareRunRoute(context.Background(), "QEMU headless")
				if hasHeadlessSchema(loop.buildToolDefs()) {
					t.Fatal("restricted registry gained absent capability")
				}
			})
		}
	}
}

type headlessReplayProvider struct {
	t         *testing.T
	viaInvoke bool
	calls     int
}

func (*headlessReplayProvider) Name() string { return "headless-fixture" }
func (p *headlessReplayProvider) Complete(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	if !hasHeadlessSchema(defs) && !requestContainsToolContract(messages, "headless_control") {
		p.t.Fatal("omitted requested schema")
	}
	ch := make(chan llm.Delta, 1)
	if p.calls == 1 {
		call := llm.ToolCall{ID: "control", Name: "headless_control", Arguments: `{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"status"}`}
		if p.viaInvoke {
			call.Name = "invoke_tool"
			call.Arguments = `{"tool":"headless_control","args":{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"status"}}`
		}
		ch <- llm.Delta{ToolCall: &call, FinishReason: "tool_calls"}
	} else {
		ch <- llm.Delta{Content: "VM running.", FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}
func TestHeadlessControlRequiresNoDiscoveryRound(t *testing.T) {
	for _, invoke := range []bool{false, true} {
		t.Run(fmt.Sprint(invoke), func(t *testing.T) {
			reg := tools.NewRegistry()
			spec := tools.NewHeadlessControl(t.TempDir(), t.TempDir()).Spec()
			calls := 0
			spec.Fn = func(context.Context, json.RawMessage) (tools.Result, error) {
				calls++
				return tools.Result{Text: `{"status":{"running":true}}`}, nil
			}
			reg.MustRegister(spec)
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn("invoke_tool")
			provider := &headlessReplayProvider{t: t, viaInvoke: invoke}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: true, StableToolset: true, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, loop, "QMP status of headless QEMU"))
			if calls != 1 || provider.calls != 2 {
				t.Fatalf("executions=%d provider=%d", calls, provider.calls)
			}
			call := llm.ToolCall{Name: "invoke_tool", Arguments: `{"tool":"headless_control","args":{}}`}
			loop.prepareRunRoute(context.Background(), "cześć")
			if _, err := resolveInvokeToolCallForRun(reg, call, ""); err == nil {
				t.Fatal("headless access leaked beyond current run")
			}
		})
	}
}
