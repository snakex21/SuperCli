package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/storage/freshness"
	"supercli/internal/system/doctor"
	"supercli/internal/tools"
)

// runStatus prints a one-shot summary of credit usage
// and the most recent audit events. Used by --status
// (F7).
func runStatus(home string, cs *credits.Storage) error {
	ctx := context.Background()
	today, err := cs.DailyTotal(ctx)
	if err != nil {
		return fmt.Errorf("daily total: %w", err)
	}
	fmt.Printf("supercli %s — status\n", version)
	fmt.Printf("home: %s\n", home)
	fmt.Printf("daily tokens (UTC day): %d\n", today)
	events, err := credits.Tail(home, 10)
	if err != nil {
		return fmt.Errorf("audit tail: %w", err)
	}
	if len(events) == 0 {
		fmt.Println("audit: (empty)")
		return nil
	}
	fmt.Println("audit (last 10 events):")
	for _, e := range events {
		ts := time.Unix(0, e.TS).UTC().Format("15:04:05")
		path := e.Path
		if path == "" {
			path = "-"
		}
		res := e.Result
		if res == "" {
			res = "?"
		}
		fmt.Printf("  %s  %-12s  %-5s  %s  %s\n",
			ts, e.Tool, res, path, truncate(e.Args, 60))
	}
	return nil
}

// truncate shortens a string to n characters with an
// ellipsis suffix. Used by --status to keep the audit
// column narrow.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// runListModels prints every model the registry
// knows about. With refresh=true, the registry is
// augmented with the /v1/models list from the
// configured provider first; the result is shown
// in-memory only (no save), so subsequent
// invocations without --refresh still see the
// user catalog + seed + probe cache as before.
//
// The output columns are deliberately compact: a
// developer running --list-models in a terminal
// wants to see the field at a glance. Detailed
// metadata lives behind --model-info.
func runListModels(caps *llm.CapabilityRegistry, baseURL, apiKey, providerName string, refresh bool) {
	if refresh {
		if baseURL == "" {
			fmt.Println("# --refresh: no base URL configured; showing cached registry only")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			ids, err := llm.ListProviderModels(ctx, baseURL, apiKey)
			cancel()
			if err != nil {
				log.Printf("list-models: provider list failed: %v", err)
			} else {
				for _, id := range ids {
					m := llm.HeuristicCapabilities(id)
					if providerName != "" {
						m.Provider = providerName
					}
					caps.Register(m)
				}
			}
		}
	}
	models := caps.All()
	if len(models) == 0 {
		fmt.Println("(no models known)")
		return
	}
	fmt.Printf("%-40s %-10s %-6s %-5s %-5s %-7s %-10s\n",
		"ID", "PROVIDER", "VISION", "TOOL", "STREAM", "REASON", "SOURCE")
	for _, m := range models {
		id := m.ID
		if len(id) > 40 {
			id = id[:37] + "..."
		}
		fmt.Printf("%-40s %-10s %-6v %-5v %-5v %-7v %-10s\n",
			id, m.Provider, m.Vision, m.ToolUse, m.Stream, m.Reasoning, m.Source)
	}
}

// runModelInfo prints detailed capability info
// for a single model id. If the registry does not
// have the model, we fall back to the heuristic
// so the user gets a best-guess answer (clearly
// labelled as such).
func runModelInfo(caps *llm.CapabilityRegistry, id string) {
	m, ok := caps.Get(id)
	heuristicOnly := false
	if !ok {
		m = llm.HeuristicCapabilities(id)
		heuristicOnly = true
	}
	fmt.Printf("ID            %s\n", m.ID)
	if m.Provider != "" {
		fmt.Printf("Provider      %s\n", m.Provider)
	}
	fmt.Printf("Vision        %v\n", m.Vision)
	fmt.Printf("ToolUse       %v\n", m.ToolUse)
	fmt.Printf("Stream        %v\n", m.Stream)
	fmt.Printf("Reasoning     %v\n", m.Reasoning)
	if m.ContextLength > 0 {
		fmt.Printf("Context       %d tokens\n", m.ContextLength)
	}
	if m.InputCost > 0 {
		fmt.Printf("Input cost    $%.4f / 1M tokens\n", m.InputCost)
	}
	if m.OutputCost > 0 {
		fmt.Printf("Output cost   $%.4f / 1M tokens\n", m.OutputCost)
	}
	if m.Notes != "" {
		fmt.Printf("Notes         %s\n", m.Notes)
	}
	if !m.LastVerified.IsZero() {
		fmt.Printf("Last verified %s\n", m.LastVerified.UTC().Format("2006-01-02"))
	}
	fmt.Printf("Source        %s\n", m.Source)
	if heuristicOnly {
		fmt.Println("Note: not in registry; showing heuristic guess. Run --refresh to learn from the provider.")
	}
}

// runDoctor prints a checklist of the things that must
// be true for SuperCli to run well. Used by --doctor
// (F7). Exits 0 even on warnings — the goal is to
// surface the situation, not to gate the user.
//
// F18: when a staleness report is provided, it is
// appended at the end of the checklist.
func runDoctor(home, dataDir string, _ *credits.Storage, report *freshness.Report) {
	rep := doctor.Run(context.Background(), doctor.Env{Version: version, Home: home, DataDir: dataDir})
	fmt.Println(doctor.RenderPlain(rep))
	// F18: staleness report from the freshness checker.
	if report != nil {
		txt := freshness.FormatReport(*report)
		if txt != "" {
			fmt.Println(txt)
		}
	}
	fmt.Println()
	fmt.Println("Use --status to inspect credit usage and the audit log.")
}

func fatal(what string, err error) {
	log.Fatalf("%s: %v", what, err)
}

// fatalUnwritableDataDir prints a clear, actionable error when the
// data directory next to the executable cannot be created or
// written (e.g. the exe sits in Program Files), then exits.
func fatalUnwritableDataDir(dir string, portable bool, err error) {
	fmt.Fprintf(os.Stderr, "ERROR: cannot write to data directory:\n  %s\n  (%v)\n\n", dir, err)
	if portable {
		fmt.Fprintln(os.Stderr, "SuperCli is portable: it stores all of its data in a supercli-data")
		fmt.Fprintln(os.Stderr, "folder next to supercli.exe. The folder the executable is in appears")
		fmt.Fprintln(os.Stderr, "to be read-only (e.g. Program Files or a network share).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Fix one of:")
		fmt.Fprintln(os.Stderr, "  - move supercli.exe to a writable folder (e.g. C:\\Tools\\supercli\\)")
		fmt.Fprintln(os.Stderr, "  - or set SUPERCLI_HOME / --home to a writable directory")
	} else {
		fmt.Fprintln(os.Stderr, "The directory configured via --home / SUPERCLI_HOME is not writable.")
		fmt.Fprintln(os.Stderr, "Point it at a writable location.")
	}
	os.Exit(1)
}

// checkDirWritable verifies the directory exists and is
// writable by creating and removing a temp file.
func checkDirWritable(dir string) error {
	tmp := filepath.Join(dir, ".write_test")
	if err := os.WriteFile(tmp, []byte("x"), 0644); err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	return os.Remove(tmp)
}

// startPostTUIShutdownTimer bounds slow cleanup after the screen has
// already been restored. It is deliberately started only after
// program.Run returns, so it can never close the live TUI like the old
// pre-run watchdog did. SQLite WAL and OS file handles remain safe if
// this fires: committed transactions are durable and the OS closes
// handles on process exit.
func startPostTUIShutdownTimer(dataDir string, d time.Duration) {
	if d <= 0 {
		return
	}
	go func() {
		defer recoverAndLog(dataDir)()
		t := time.NewTimer(d)
		defer t.Stop()
		<-t.C
		os.Exit(0)
	}()
}

// discoverSkillsForDoctor scans the home directory for
// SKILL.md files and returns freshness.SkillEntry values
// with file mtimes. Used by --doctor to report stale
// skills without requiring a running provider.
func discoverSkillsForDoctor(home, dataDir string) []freshness.SkillEntry {
	d := tools.NewDiscovererWithBuiltins(home, dataDir)
	skills, err := d.Discover()
	if err != nil {
		return nil
	}
	out := make([]freshness.SkillEntry, 0, len(skills))
	for _, s := range skills {
		fi, err := os.Stat(s.Path)
		if err != nil {
			continue
		}
		out = append(out, freshness.SkillEntry{
			Name:     s.Name,
			Path:     s.Path,
			Modified: fi.ModTime(),
		})
	}
	return out
}
