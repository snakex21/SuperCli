package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/tools"
	"supercli/internal/tools/mcp"
	"supercli/internal/ui/tui"
)

// mcpStartTimeout bounds spawning + initialize + tools/list per server.
// npx-based servers may need to download packages on first run.
const mcpStartTimeout = 60 * time.Second

// initMcp merges config.toml servers with portable packages discovered under
// <dataDir>/mcp. It registers one small bridge tool and deliberately starts no
// subprocesses: a server is launched only when the model searches or calls it.
func initMcp(dataDir string, tomlCfg config.TomlConfig, registry *tools.Registry, reindex func()) *mcp.Manager {
	configs := make(map[string]mcp.ServerConfig, len(tomlCfg.Mcp.Servers))
	for name, s := range tomlCfg.Mcp.Servers {
		configs[name] = mcp.ServerConfig{Command: s.Command, Args: s.Args, Env: s.Env}
	}
	merged, packages, err := mcp.LoadWorkspace(dataDir, configs)
	if err != nil {
		log.Printf("mcp: portable workspace discovery: %v", err)
	} else {
		configs = merged
	}
	for _, pkg := range packages {
		if pkg.Error != "" {
			log.Printf("mcp: portable package %s unavailable: %s", pkg.ID, pkg.Error)
		}
	}
	if len(configs) == 0 {
		return nil
	}
	manager := mcp.NewManager(configs)
	registry.MustRegister(mcp.NewBridge(manager).Spec())
	registry.MarkAlwaysOn("mcp_bridge")
	if reindex != nil {
		reindex()
	}
	return manager
}

// mcpCommand returns the /mcp slash handler: list servers and their
// status, or "/mcp restart <name>" to stop+start one server.
func mcpCommand(manager *mcp.Manager) tui.SlashHandler {
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
			return fmt.Sprintf("mcp: %s restarted and ready", name), nil
		}
		var b strings.Builder
		b.WriteString("MCP servers:\n")
		for _, st := range manager.Statuses() {
			state := "stopped"
			if st.Running {
				state = fmt.Sprintf("running %s", st.Uptime.Round(time.Second))
			}
			kind := "configured"
			if st.Portable {
				kind = "portable"
			}
			fmt.Fprintf(&b, "  %-14s %-18s %d tool(s)  %s  cmd: %s\n", st.Name, state, st.Tools, kind, st.Command)
			if st.Err != "" {
				fmt.Fprintf(&b, "                 error: %s\n", st.Err)
			}
		}
		b.WriteString("\nservers start lazily through mcp_bridge only when the model needs them\n")
		b.WriteString("restart: /mcp restart <name>\n")
		return b.String(), nil
	}
}
