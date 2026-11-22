package workflow

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/tools/ctxexec"
)

// A command that hangs (a stuck compiler, a test waiting forever) is killed
// at the configured default limit when the model passed no timeout_ms, and
// the model is told plainly what happened (1.0.39).
func TestCtxExecuteDefaultTimeoutKillsHungCommand(t *testing.T) {
	root := t.TempDir()
	tool := NewCtxExecuteTool(ctxexec.New(root), root)
	tool.DefaultTimeoutMS = 700
	reg := NewRegistry()
	reg.MustRegister(tool.Spec())
	args, _ := json.Marshal(map[string]any{
		"command":   []string{os.Args[0], "-test.run=^TestCtxTimeoutHelper$"},
		"env_extra": []string{"SUPERCLI_TIMEOUT_HELPER=wait"}, // sleeps 10 s
	})
	started := time.Now()
	result, _ := reg.Execute(context.Background(), "ctx_execute", args)
	if took := time.Since(started); took > 6*time.Second {
		t.Fatalf("the hung command was not killed at the default limit (took %v)", took)
	}
	if result.Err == nil {
		t.Fatal("a killed command reported success")
	}
	msg := result.Err.Error()
	if !strings.Contains(msg, "command killed") || !strings.Contains(msg, "command_timeout_ms") || strings.Contains(result.Text, "FINISHED") {
		t.Fatalf("unclear timeout message: %s", msg)
	}
}

// An explicit timeout_ms still wins over the default.
func TestCtxExecuteExplicitTimeoutOverridesDefault(t *testing.T) {
	root := t.TempDir()
	tool := NewCtxExecuteTool(ctxexec.New(root), root)
	tool.DefaultTimeoutMS = 300
	reg := NewRegistry()
	reg.MustRegister(tool.Spec())
	result, err := reg.Execute(context.Background(), "ctx_execute", ctxTimeoutArgs(t, "quick", 60000))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "FINISHED") {
		t.Fatalf("explicit timeout lost: %v / %v", err, result.Err)
	}
}

func TestCommandTimeoutConfig(t *testing.T) {
	var unset config.TomlConfig
	if got := unset.CommandTimeout(); got != config.DefaultCommandTimeoutMS {
		t.Fatalf("default = %d", got)
	}
	if got := (&config.TomlConfig{CommandTimeoutMS: -1}).CommandTimeout(); got != 0 {
		t.Fatalf("-1 should disable the limit, got %d", got)
	}
	if got := (&config.TomlConfig{CommandTimeoutMS: 90000}).CommandTimeout(); got != 90000 {
		t.Fatalf("configured = %d", got)
	}
}
