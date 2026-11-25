package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"supercli/internal/tools"
)

// Coordinator commands in the background (after 1.0.40). The audited
// coordinator ran ctx_execute jobs of 321-749 s (timeout_ms 3600000) while
// its workers' reports waited until each command ended. A coordinator in
// orchestrator mode, or one with background workers running, now waits at
// most coordinatorForegroundMS for a command: a command still running then
// continues in the background with a handle, the turn goes on, and its
// completion (exit code and output tail) arrives as a notification, like a
// worker report. Only a runaway command is killed, after
// coordinatorRunawayMS.
const (
	coordinatorForegroundMS = 120_000
	coordinatorRunawayMS    = 2 * 60 * 60 * 1000
)

// coordinatorForeground is the foreground wait (a variable for tests).
var coordinatorForeground = time.Duration(coordinatorForegroundMS) * time.Millisecond

var backgroundCommandSeq atomic.Int64

// capCoordinatorCommand reports whether a coordinator ctx_execute goes
// through the background fallback, and returns its arguments with the
// runaway limit as timeout (a shorter explicit timeout is kept). Workers and
// a plain coordinator without workers run commands as before.
func (l *Loop) capCoordinatorCommand(name string, raw json.RawMessage) (json.RawMessage, bool) {
	if name != "ctx_execute" || l.route != RouteCoordinator || !(l.orchestrator || l.backgroundActive.Load() > 0) {
		return raw, false
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return raw, false
	}
	var timeout int64
	if v, ok := args["timeout_ms"]; ok {
		_ = json.Unmarshal(v, &timeout)
	}
	if timeout > 0 && timeout <= coordinatorForegroundMS {
		return raw, false // fits the foreground wait: an ordinary command
	}
	if timeout <= 0 || timeout > coordinatorRunawayMS {
		args["timeout_ms"] = json.RawMessage(fmt.Sprint(coordinatorRunawayMS))
	}
	out, err := json.Marshal(args)
	if err != nil {
		return raw, false
	}
	return out, true
}

// executeWithBackgroundFallback runs exec; if it has not finished after the
// foreground wait, the call returns a handle and the command keeps running.
// Its completion is delivered to the conversation as a background report.
func (l *Loop) executeWithBackgroundFallback(ctx context.Context, command string, exec func(context.Context) (tools.Result, error)) (tools.Result, error) {
	type outcome struct {
		res tools.Result
		err error
	}
	done := make(chan outcome, 1)
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(coordinatorRunawayMS)*time.Millisecond+time.Minute)
	deliver := l.backgroundDelivery(ctx)
	started := time.Now()
	go func() {
		res, err := exec(runCtx)
		done <- outcome{res, err}
	}()
	timer := time.NewTimer(coordinatorForeground)
	defer timer.Stop()
	select {
	case o := <-done:
		cancel()
		return o.res, o.err
	case <-ctx.Done():
		cancel()
		o := <-done
		return o.res, o.err
	case <-timer.C:
	}
	handle := fmt.Sprintf("bgcmd-%d", backgroundCommandSeq.Add(1))
	l.backgroundActive.Add(1)
	go func() {
		defer cancel()
		o := <-done
		l.backgroundActive.Add(-1)
		deliver(context.Background(), backgroundCommandReport(handle, command, o.res, o.err, time.Since(started)))
	}()
	return tools.Result{Text: fmt.Sprintf("<background-command>\n<id>%s</id>\n<status>running</status>\n<command>%s</command>\n"+
		"<note>Still running after %s, so it continues in the background instead of blocking this turn and worker reports. "+
		"Its exit code and output arrive automatically as a notification; continue other work or end this response to wait. Do not start it again.</note>\n</background-command>",
		handle, command, coordinatorForeground.Round(time.Second))}, nil
}

// backgroundCommandReport is the completion notification of a background
// command: exit status, duration and the end of its output.
func backgroundCommandReport(handle, command string, res tools.Result, err error, took time.Duration) string {
	status, exit := "completed", "0"
	if err == nil {
		err = res.Err
	}
	if err != nil {
		status = "failed"
		exit = "nonzero"
		var parsed struct {
			ExitCode *int `json:"exit_code"`
		}
		if json.Unmarshal([]byte(res.Text), &parsed) == nil && parsed.ExitCode != nil {
			exit = fmt.Sprint(*parsed.ExitCode)
		}
	}
	output := res.Text
	if err != nil {
		output = err.Error()
	}
	if len(output) > 4000 {
		output = "..." + output[len(output)-4000:]
	}
	return fmt.Sprintf("<background-command-notification>\n<id>%s</id>\n<status>%s</status>\n<exit-code>%s</exit-code>\n<duration>%s</duration>\n<command>%s</command>\n<output>%s</output>\n</background-command-notification>",
		handle, status, exit, took.Round(time.Second), command, strings.TrimSpace(output))
}

// commandText is a command's argv joined for notices.
func commandText(args string) string {
	var a struct {
		Command []string `json:"command"`
	}
	if json.Unmarshal([]byte(args), &a) != nil || len(a.Command) == 0 {
		return "command"
	}
	text := strings.Join(a.Command, " ")
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return strings.NewReplacer("<", "(", ">", ")").Replace(text)
}
