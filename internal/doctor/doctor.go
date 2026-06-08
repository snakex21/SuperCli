// Package doctor builds runtime diagnostics for SuperCli.
package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/providers"
	"supercli/internal/session"
	"supercli/internal/tools"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

type Check struct {
	Name        string
	Status      Status
	Detail      string
	Remediation string
}

type Report struct {
	GeneratedAt time.Time
	Version     string
	Checks      []Check
}

type Env struct {
	Version     string
	Home        string
	DataDir     string
	Provider    llm.Provider
	Registry    *tools.Registry
	Sessions    *session.Store
	ProviderMgr *providers.Manager
	Caps        *llm.CapabilityRegistry
}

func Run(ctx context.Context, env Env) Report {
	if env.Version == "" {
		env.Version = "dev"
	}
	checks := []Check{
		binaryCheck(),
		goRuntimeCheck(),
		dirCheck("home", env.Home, true),
		dirCheck("data dir", env.DataDir, true),
		fileCheck("sqlite db", filepath.Join(env.DataDir, "supercli.db"), false),
		sessionsCheck(ctx, env.Sessions),
		providerCheck(env.Provider),
		providerConfigCheck(env.ProviderMgr, env.Caps),
		toolsCheck(env.Registry),
		commandCheck("git", true),
		commandCheck("rg", false),
	}
	return Report{GeneratedAt: time.Now(), Version: env.Version, Checks: checks}
}

func (r Report) Summary() (ok, warn, fail, skip int) {
	for _, c := range r.Checks {
		switch c.Status {
		case OK:
			ok++
		case Warn:
			warn++
		case Fail:
			fail++
		case Skip:
			skip++
		}
	}
	return
}

func binaryCheck() Check {
	exe, err := os.Executable()
	if err != nil {
		return Check{Name: "binary", Status: Warn, Detail: err.Error()}
	}
	return Check{Name: "binary", Status: OK, Detail: exe}
}

func goRuntimeCheck() Check {
	return Check{Name: "runtime", Status: OK, Detail: runtime.Version() + " · " + runtime.GOOS + "/" + runtime.GOARCH}
}

func dirCheck(name, path string, writable bool) Check {
	if path == "" {
		return Check{Name: name, Status: Fail, Detail: "not configured", Remediation: "set --home or SUPERCLI_HOME"}
	}
	info, err := os.Stat(path)
	if err != nil {
		return Check{Name: name, Status: Fail, Detail: path + " · " + err.Error(), Remediation: "create the directory or choose another --home"}
	}
	if !info.IsDir() {
		return Check{Name: name, Status: Fail, Detail: path + " is not a directory"}
	}
	if writable && !isWritable(path) {
		return Check{Name: name, Status: Fail, Detail: path + " is not writable", Remediation: "fix permissions or choose another --home"}
	}
	return Check{Name: name, Status: OK, Detail: path}
}

func fileCheck(name, path string, required bool) Check {
	if path == "" {
		return Check{Name: name, Status: Skip, Detail: "not configured"}
	}
	info, err := os.Stat(path)
	if err != nil {
		if required {
			return Check{Name: name, Status: Fail, Detail: path + " missing", Remediation: "start SuperCli once to initialize storage"}
		}
		return Check{Name: name, Status: Warn, Detail: path + " not created yet"}
	}
	if info.IsDir() {
		return Check{Name: name, Status: Fail, Detail: path + " is a directory"}
	}
	return Check{Name: name, Status: OK, Detail: fmt.Sprintf("%s · %s", path, humanSize(info.Size()))}
}

func sessionsCheck(ctx context.Context, store *session.Store) Check {
	if store == nil {
		return Check{Name: "sessions", Status: Warn, Detail: "session store unavailable", Remediation: "check sessions.db permissions"}
	}
	sessions, err := store.List(5)
	if err != nil {
		status := Warn
		if err == sql.ErrNoRows {
			status = OK
		}
		return Check{Name: "sessions", Status: status, Detail: err.Error()}
	}
	_ = ctx
	return Check{Name: "sessions", Status: OK, Detail: fmt.Sprintf("%d recent session(s) visible", len(sessions))}
}

func providerCheck(p llm.Provider) Check {
	if p == nil {
		return Check{Name: "provider", Status: Fail, Detail: "no provider wired", Remediation: "configure provider/model or use --echo"}
	}
	name := p.Name()
	status := OK
	remediation := ""
	if strings.Contains(strings.ToLower(name), "echo") {
		status = Warn
		remediation = "configure a real provider for AI responses"
	}
	return Check{Name: "provider", Status: status, Detail: name, Remediation: remediation}
}

func providerConfigCheck(mgr *providers.Manager, caps *llm.CapabilityRegistry) Check {
	if mgr == nil {
		return Check{Name: "provider config", Status: Skip, Detail: "manager not wired"}
	}
	names := mgr.Names()
	models := 0
	if caps != nil {
		models = len(caps.All())
	}
	if len(names) == 0 {
		return Check{Name: "provider config", Status: Warn, Detail: fmt.Sprintf("no configured providers · %d known model(s)", models), Remediation: "use /providers add or edit .supercli/config.toml"}
	}
	return Check{Name: "provider config", Status: OK, Detail: fmt.Sprintf("%d provider(s) · %d known model(s)", len(names), models)}
}

func toolsCheck(reg *tools.Registry) Check {
	if reg == nil {
		return Check{Name: "tools", Status: Warn, Detail: "registry not wired"}
	}
	visible := len(reg.VisibleNames())
	status := OK
	if visible == 0 {
		status = Warn
	}
	return Check{Name: "tools", Status: status, Detail: fmt.Sprintf("%d registered · %d visible", reg.Len(), visible)}
}

func commandCheck(name string, required bool) Check {
	_, err := exec.LookPath(name)
	if err == nil {
		return Check{Name: name + " command", Status: OK, Detail: "found in PATH"}
	}
	if required {
		return Check{Name: name + " command", Status: Warn, Detail: "not found in PATH", Remediation: "install " + name + " or add it to PATH"}
	}
	return Check{Name: name + " command", Status: Skip, Detail: "optional · not found"}
}

func isWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
