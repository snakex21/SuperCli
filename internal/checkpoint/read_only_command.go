package checkpoint

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"supercli/internal/tools/ctxexec"
)

// A small allowlist keeps ordinary repository inspection from snapshotting an
// entire asset/source tree. Other shell commands, scripts, custom Git
// configuration and unrecognized flags keep the full checkpoint path. This is
// about workspace files; Git's own index refresh is outside the undo scope.
func checkpointCommandReadOnly(name string, raw json.RawMessage) bool {
	_, readOnly := checkpointReadOnlyCommandContext(context.Background(), name, raw)
	return readOnly
}

// Bind native listings at the shared wrapper, before a snapshot is bypassed.
func checkpointReadOnlyContext(ctx context.Context, name string, raw json.RawMessage) (context.Context, bool) {
	if name == "ctx_execute" {
		return checkpointReadOnlyCommandContext(ctx, name, raw)
	}
	return ctx, checkpointToolReadOnly(name, raw)
}

func checkpointReadOnlyCommandContext(ctx context.Context, name string, raw json.RawMessage) (context.Context, bool) {
	if name != "ctx_execute" {
		return ctx, false
	}
	var args struct {
		Command  []string `json:"command"`
		EnvExtra []string `json:"env_extra"`
		Workdir  string   `json:"workdir"`
	}
	if json.Unmarshal(raw, &args) != nil || len(args.Command) == 0 {
		return ctx, false
	}
	command := args.Command
	if runtime.GOOS == "windows" {
		if listing, ok := ctxexec.ResolveWindowsDirCommand(command, args.EnvExtra, os.Getenv("SystemRoot"), exec.LookPath); ok {
			return ctxexec.WithWindowsDirCommand(ctx, listing, args.Workdir), true
		}
	}
	// Explicit paths may identify a project script named git rather than the
	// normal PATH executable. Do not infer its behavior from a basename.
	exe := strings.ToLower(command[0])
	if (exe != "git" && exe != "git.exe") || len(command) < 2 {
		return ctx, false
	}
	switch command[1] {
	case "status":
		for _, arg := range command[2:] {
			switch arg {
			case "-s", "--short", "-b", "--branch", "-sb", "-bs", "--porcelain", "--porcelain=v1", "--porcelain=v2", "-uno", "-unormal", "-uall", "--untracked-files=no", "--untracked-files=normal", "--untracked-files=all", "--ignored", "--no-renames":
			default:
				return ctx, false
			}
		}
		return ctx, true
	case "rev-parse":
		if len(command) != 3 {
			return ctx, false
		}
		switch command[2] {
		case "HEAD", "--show-toplevel", "--is-inside-work-tree", "--show-prefix", "--git-dir", "--is-bare-repository":
			return ctx, true
		}
	case "branch":
		// No arguments lists branches; any creation, deletion, tracking or
		// movement flags are intentionally absent.
		for _, arg := range command[2:] {
			if arg != "-a" && arg != "--all" && arg != "-r" && arg != "--remotes" && arg != "--list" && arg != "-v" && arg != "-vv" {
				return ctx, false
			}
		}
		return ctx, true
	case "log":
		for i := 2; i < len(command); i++ {
			arg := command[i]
			switch arg {
			case "--oneline", "--all", "--decorate", "--no-decorate", "--graph", "--stat", "--name-only", "--name-status":
			case "-n", "--max-count":
				i++
				if i >= len(command) || !positiveInteger(command[i]) {
					return ctx, false
				}
			default:
				if !strings.HasPrefix(arg, "-") || !positiveInteger(strings.TrimPrefix(arg, "-")) {
					return ctx, false
				}
			}
		}
		return ctx, true
	}
	return ctx, false
}

// The same resolver prepares execution and admission, including custom dir
// detection. Production wrappers also bind the returned native mapping.
func checkpointWindowsDirReadOnly(command, envExtra []string, systemRoot string, lookPath func(string) (string, error)) bool {
	_, ok := ctxexec.ResolveWindowsDirCommand(command, envExtra, systemRoot, lookPath)
	return ok
}

func positiveInteger(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(value)
	return err == nil && n > 0
}
