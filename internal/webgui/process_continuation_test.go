package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/llm/prompt"
)

func TestWebProcessContinuationHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_WEB_PROCESS_HELPER") != "1" {
		return
	}
	conn, err := net.DialTimeout("tcp", os.Getenv("SUPERCLI_WEB_PROCESS_READY"), 10*time.Second)
	if err != nil {
		os.Exit(99)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		os.Exit(98)
	}
	_ = conn.Close()
	fmt.Println("PROCESS_COMPLETED_AFTER_FIRST_WEB_TURN")
	if os.Getenv("SUPERCLI_WEB_PROCESS_FAIL") == "1" {
		os.Exit(7)
	}
	os.Exit(0)
}

type webProcessContinuationProvider struct {
	thin         bool
	command, env []string
	id           string
	calls        int
}

func (p *webProcessContinuationProvider) Name() string { return "echo-test" }
func (p *webProcessContinuationProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	step := p.calls
	p.calls++
	if step == 0 {
		thinPrompt := false
		for _, msg := range msgs {
			thinPrompt = thinPrompt || strings.Contains(msg.Content, prompt.ThinToolProtocol)
		}
		if thinPrompt != p.thin {
			return nil, fmt.Errorf("incorrect web tool profile: thin=%t want=%t", thinPrompt, p.thin)
		}
	}
	if step == 3 && !p.thin && !slices.ContainsFunc(defs, func(d llm.ToolDef) bool { return d.Name == "process_session" }) {
		return nil, fmt.Errorf("native process schema was not restored for the second web run")
	}
	var call *llm.ToolCall
	makeCall := func(id, name string, args any) {
		raw, _ := json.Marshal(args)
		call = &llm.ToolCall{ID: id, Name: name, Arguments: string(raw)}
		if p.thin && name == "process_session" {
			raw, _ = json.Marshal(map[string]any{"tool": name, "args": args})
			call.Name, call.Arguments = "invoke_tool", string(raw)
		}
	}
	text := ""
	switch step {
	case 0:
		makeCall("discover-process", "tool_search", map[string]any{"query": "process_session", "limit": 1})
	case 1:
		makeCall("start-process", "process_session", map[string]any{"action": "start", "command": p.command, "env": p.env, "yield_ms": 0})
	case 2:
		for _, msg := range msgs {
			if msg.Role == llm.RoleTool && msg.ToolCallID == "start-process" {
				var snap struct{ ID, Status string }
				if err := json.Unmarshal([]byte(msg.Content), &snap); err != nil {
					return nil, fmt.Errorf("start snapshot: %w: %s", err, msg.Content)
				}
				if snap.ID == "" || snap.Status != "running" {
					return nil, fmt.Errorf("process not running at handoff: %+v", snap)
				}
				p.id = snap.ID
			}
		}
		if p.id == "" {
			return nil, fmt.Errorf("missing started process")
		}
		text = "Process " + p.id + " is running."
	case 3:
		makeCall("wait-process", "process_session", map[string]any{"action": "wait", "id": p.id})
	case 4:
		text = "Process outcome received."
	default:
		return nil, fmt.Errorf("unexpected model request %d", step)
	}
	out := make(chan llm.Delta, 2)
	if call != nil {
		out <- llm.Delta{ToolCall: call}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Content: text}
		out <- llm.Delta{FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

func TestWebProcessSurvivesNewRunWithoutRediscovery(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, exit := range []int{0, 7} {
			t.Run(fmt.Sprintf("thin=%t/exit=%d", thin, exit), func(t *testing.T) {
				root := t.TempDir()
				settings := fmt.Sprintf("preflight_repo = false\nnavigator = 'off'\nsmall_full_tools = %t\nstable_toolset = true\n", !thin)
				if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(settings), 0600); err != nil {
					t.Fatal(err)
				}
				ready, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer ready.Close()
				_ = ready.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
				eng, err := NewEngine(echoConfig(), root, root)
				if err != nil {
					t.Fatal(err)
				}
				defer eng.Close()
				fail := "0"
				if exit != 0 {
					fail = "1"
				}
				provider := &webProcessContinuationProvider{thin: thin, command: []string{os.Args[0], "-test.run=^TestWebProcessContinuationHelper$"}, env: []string{"SUPERCLI_WEB_PROCESS_HELPER=1", "SUPERCLI_WEB_PROCESS_READY=" + ready.Addr().String(), "SUPERCLI_WEB_PROCESS_FAIL=" + fail}}
				eng.prov = provider
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				sid := ""
				discovery, starts, waits := 0, 0, 0
				var terminal wireEvent
				collect := func(ev wireEvent) {
					if ev.Type == "session" {
						sid = ev.SessionID
					}
					if ev.Type == "error" {
						t.Errorf("web error: %s", ev.Err)
					}
					if ev.Type == "tool_call" {
						switch ev.ID {
						case "discover-process":
							discovery++
						case "start-process":
							starts++
						case "wait-process":
							waits++
						}
					}
					if ev.Type == "tool_result" && ev.ID == "wait-process" {
						terminal = ev
					}
				}
				err = eng.runStream(ctx, "Implement process start-fixture", "", "", collect)
				cancel() // End the first request's context before the process completes.
				if err != nil {
					t.Fatal(err)
				}
				if sid == "" || provider.id == "" {
					t.Fatal("session or process handle missing")
				}
				store, err := eng.sessionStore()
				if err != nil {
					t.Fatal(err)
				}
				discovered, err := store.ReadDiscoveredTools(context.Background(), sid)
				if err != nil || !slices.Contains(discovered, "process_session") {
					t.Fatalf("discovery not saved: %v %v", discovered, err)
				}
				original := eng.processSessionFor(root)
				alias := root + string(filepath.Separator) + "."
				if runtime.GOOS == "windows" {
					alias = strings.ToUpper(alias)
				}
				if eng.processSessionFor(alias) != original {
					t.Fatal("equivalent workspace started a second process manager")
				}
				other := eng.processSessionFor(t.TempDir())
				if other == original {
					t.Fatal("different workspaces share process handles")
				}
				if _, err := other.Manager.Wait(context.Background(), provider.id); err == nil {
					t.Fatal("other workspace accessed the process")
				}
				// Release once, then wait on the process completion channel through the
				// second web turn. No polling and no relaunch of the helper.
				conn, err := ready.Accept()
				if err != nil {
					t.Fatal(err)
				}
				_, err = conn.Write([]byte{1})
				_ = conn.Close()
				if err != nil {
					t.Fatal(err)
				}
				ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel2()
				if err := eng.runStream(ctx2, "Implement process wait-fixture", sid, "", collect); err != nil {
					t.Fatal(err)
				}
				var snap struct {
					ID, Status, Stdout string
					ExitCode           *int `json:"exit_code"`
				}
				if err := json.Unmarshal([]byte(terminal.Output), &snap); err != nil {
					t.Fatalf("missing terminal snapshot: %s %v", terminal.Output, err)
				}
				status := "done"
				if exit != 0 {
					status = "failed"
				}
				if snap.ID != provider.id || snap.Status != status || snap.ExitCode == nil || *snap.ExitCode != exit || !strings.Contains(snap.Stdout, "PROCESS_COMPLETED_AFTER_FIRST_WEB_TURN") {
					t.Fatalf("process state lost: %+v", snap)
				}
				if (terminal.Err != "") != (exit != 0) {
					t.Fatalf("incorrect UI outcome: %s", terminal.Err)
				}
				if discovery != 1 || starts != 1 || waits != 1 || provider.calls != 5 {
					t.Fatalf("repeated work: discovery=%d starts=%d waits=%d calls=%d", discovery, starts, waits, provider.calls)
				}
				t.Logf("two web runs: one discovery, one process start, one completion wait; %s exit=%d", snap.Status, exit)
			})
		}
	}
}
