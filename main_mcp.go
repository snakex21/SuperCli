package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"supercli/internal/config"
	"supercli/internal/mcp"
	"supercli/internal/tools"
	"supercli/internal/tui"
)

// mcpStartTimeout bounds spawning + initialize + tools/list per server.
// npx-based servers may need to download packages on first run.
const mcpStartTimeout = 60 * time.Second

// initMcp builds the MCP manager from config.toml [mcp.servers.*]
// sections and starts the servers in the background. Discovered tools
// are registered as mcp_<server>_<tool>, deferred (NOT always-on): the
// model pulls their schemas through tool_search, so MCP never bloats
// the default context. Returns nil when no servers are configured.
func initMcp(tomlCfg config.TomlConfig, registry *tools.Registry, reindex func()) *mcp.Manager {
	if len(tomlCfg.Mcp.Servers) == 0 {
		return nil
	}
	configs := make(map[string]mcp.ServerConfig, len(tomlCfg.Mcp.Servers))
	for name, s := range tomlCfg.Mcp.Servers {
		configs[name] = mcp.ServerConfig{Command: s.Command, Args: s.Args, Env: s.Env}
	}
	manager := mcp.NewManager(configs)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), mcpStartTimeout)
		defer cancel()
		errs := manager.StartAll(ctx)
		for name, err := range errs {
			log.Printf("mcp: server %s failed to start: %v", name, err)
		}
		if n := mcp.RegisterTools(manager, registry); n > 0 {
			log.Printf("mcp: registered %d tool(s) from %d server(s)", n, len(configs))
			if reindex != nil {
				reindex()
			}
		}
	}()
	return manager
}

// mcpCommand returns the /mcp slash handler: list servers and their
// status, or "/mcp restart <name>" to stop+start one server.
func mcpCommand(manager *mcp.Manager, registry *tools.Registry, reindex func()) tui.SlashHandler {
	return func(ctx context.Context, args string) (string, error) {
		if manager == nil {
			return "mcp: no servers configured.\nAdd to config.toml:\n" +
				"  [mcp.servers.context7]\n" +
				"  command = \"npx\"\n" +
				"  args = [\"-y\", \"@upstash/context7-mcp\"]", nil
		}
		fields := strings.Fields(args)
		if len(fields) >= 1 && strings.EqualFold(fields[0], "restart") {
			if len(fields) < 2 {
				return "usage: /mcp restart <name>", nil
			}
			name := fields[1]
			rctx, cancel := context.WithTimeout(ctx, mcpStartTimeout)
			defer cancel()
			if err := manager.Restart(rctx, name); err != nil {
				return fmt.Sprintf("mcp: restart %s: %v", name, err), nil
			}
			n := mcp.RegisterTools(manager, registry) // re-register any new tools
			if n > 0 && reindex != nil {
				reindex()
			}
			return fmt.Sprintf("mcp: %s restarted (%d new tool(s) registered)", name, n), nil
		}
		var b strings.Builder
		b.WriteString("MCP servers:\n")
		for _, st := range manager.Statuses() {
			state := "stopped"
			if st.Running {
				state = fmt.Sprintf("running %s", st.Uptime.Round(time.Second))
			}
			fmt.Fprintf(&b, "  %-14s %-18s %d tool(s)  cmd: %s\n", st.Name, state, st.Tools, st.Command)
			if st.Err != "" {
				fmt.Fprintf(&b, "                 error: %s\n", st.Err)
			}
		}
		b.WriteString("\ntools are named mcp_<server>_<tool>; the model finds them via tool_search\n")
		b.WriteString("restart: /mcp restart <name>\n")
		return b.String(), nil
	}
}
