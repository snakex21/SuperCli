// Command startup-dataset fills a SuperCli data directory with synthetic
// conversations for measuring GUI start-up: many sessions, and one long
// session (thousands of messages with tool calls and results), like a real
// working history. It never touches an existing data directory's sessions
// unless that directory is given explicitly.
//
//	go run ./scripts/startup-dataset -data DIR -sessions 300 -long 6000
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func main() {
	dataDir := flag.String("data", "", "data directory to fill (required)")
	sessions := flag.Int("sessions", 300, "number of ordinary sessions")
	perSession := flag.Int("messages", 40, "messages in each ordinary session")
	long := flag.Int("long", 6000, "messages in the long session (0 = none)")
	cwd := flag.String("cwd", "", "project folder the sessions belong to (default: DATA/workspace, as startup-bench uses)")
	flag.Parse()
	if *dataDir == "" {
		log.Fatal("-data is required")
	}
	if *cwd == "" {
		*cwd = filepath.Join(*dataDir, "workspace")
	}
	if abs, err := filepath.Abs(*cwd); err == nil {
		*cwd = abs
	}
	if err := os.MkdirAll(*cwd, 0o755); err != nil {
		log.Fatal(err)
	}
	store, err := session.OpenStore(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	started := time.Now()
	for i := 0; i < *sessions; i++ {
		s, err := store.Create(*cwd, "fixture-model", fmt.Sprintf("Rozmowa %d: poprawki w module %d", i+1, i%17))
		if err != nil {
			log.Fatal(err)
		}
		fill(ctx, store, s.ID, *perSession)
	}
	if *long > 0 {
		s, err := store.Create(*cwd, "fixture-model", "Port Minecrafta 1.12 do Ziga")
		if err != nil {
			log.Fatal(err)
		}
		fill(ctx, store, s.ID, *long)
	}
	log.Printf("filled %s: %d sessions x %d messages + one with %d in %s", *dataDir, *sessions, *perSession, *long, time.Since(started).Round(time.Millisecond))
}

// fill appends n messages as user / assistant-with-tool-call / tool result /
// assistant answer rounds.
func fill(ctx context.Context, store *session.Store, id string, n int) {
	text := strings.Repeat("Sprawdzam moduł świata i kodek gracza; poprawiam odczyt pancerza. ", 6)
	output := strings.Repeat("native_world.zig:88: const armor = bag.armor.stats().points;\n", 40)
	for i := 0; i < n; {
		call := llm.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "read_lines", Arguments: mustJSON(map[string]any{"path": "zig-engine/src/native_world.zig", "from": 1, "to": 120})}
		round := []llm.Message{
			{Role: llm.RoleUser, Content: fmt.Sprintf("Krok %d: sprawdź pancerz i testy.", i/4+1)},
			{Role: llm.RoleAssistant, Content: "Czytam plik.", ToolCalls: []llm.ToolCall{call}},
			{Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: output},
			{Role: llm.RoleAssistant, Content: text},
		}
		for _, m := range round {
			if i >= n {
				return
			}
			enc, err := session.FromMessage(m)
			if err != nil {
				log.Fatal(err)
			}
			if err := store.AppendMessage(ctx, id, enc); err != nil {
				log.Fatal(err)
			}
			i++
		}
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
