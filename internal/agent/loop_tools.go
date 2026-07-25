package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// toolResult is the internal envelope for a single tool execution.
type toolResult struct {
	followUps []llm.Message
	fatal     bool
	failed    bool
	// inert: the call succeeded but changed nothing that did not already
	// exist. Kept apart from failed — the model is told the truth (it
	// worked) while progress accounting refuses to bank it.
	inert bool
	err   error
}

// invokeToolCalls runs the model's tool-call batch and appends the matching
// tool-result messages to history. Most tools are executed sequentially to
// avoid surprising write conflicts. A batch made only of the coordinator's
// `task` calls can be run concurrently: each task owns an isolated child
// loop/context. Whether it actually runs in parallel is gated by
// taskParallel — on a single local GPU the workers serialize on one server
// slot anyway (N× wall time) and interleaved contexts thrash the KV cache,
// so local backends default to sequential; cloud backends run parallel.
//
// The third return is how many calls succeeded inertly (see toolResult.inert).
func (l *Loop) invokeToolCalls(ctx context.Context, toolCalls []llm.ToolCall, out chan<- Event) (bool, int, int) {
	// Independent reads do not touch the model backend and cannot conflict
	// with one another. Run them concurrently on both local and cloud setups;
	// the results are still appended in call order for a stable prompt.
	if len(toolCalls) > 1 && l.allReadOnlyCalls(toolCalls) {
		return l.invokeCallsParallel(ctx, toolCalls, out)
	}
	if len(toolCalls) > 1 && allTaskCalls(toolCalls) && l.taskParallel {
		if l.taskParallelWarnLocal && !l.taskParallelWarned {
			l.taskParallelWarned = true
			out <- NoticeEvent{Text: fmt.Sprintf(
				"running %d task workers in parallel on a local backend — they serialize on one GPU slot (~%d× time) and thrash each other's KV cache; set task_parallel = false for sequential",
				len(toolCalls), len(toolCalls))}
		}
		return l.invokeCallsParallel(ctx, toolCalls, out)
	}

	failures, inert := 0, 0
	for _, tc := range toolCalls {
		ev := l.invoke(ctx, tc, out)
		if ev.failed {
			failures++
		}
		if ev.inert {
			inert++
		}
		for _, m := range ev.followUps {
			l.Messages = append(l.Messages, m)
			l.persist(ctx, m)
		}
		if ev.fatal {
			out <- ErrorEvent{Err: ev.err}
			return false, failures, inert
		}
	}
	return true, failures, inert
}

func allTaskCalls(toolCalls []llm.ToolCall) bool {
	for _, tc := range toolCalls {
		if tc.Name != "task" {
			return false
		}
	}
	return len(toolCalls) > 0
}

func (l *Loop) allReadOnlyCalls(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) == 0 {
		return false
	}
	for _, call := range toolCalls {
		tool, ok := l.registry.Get(call.Name)
		if !ok || !tool.ReadOnly {
			return false
		}
	}
	return true
}

func (l *Loop) invokeCallsParallel(ctx context.Context, toolCalls []llm.ToolCall, out chan<- Event) (bool, int, int) {
	type item struct {
		idx int
		res toolResult
	}
	results := make([]toolResult, len(toolCalls))
	ch := make(chan item, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		i, tc := i, tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch <- item{idx: i, res: l.invoke(ctx, tc, out)}
		}()
	}

	wg.Wait()
	close(ch)
	for it := range ch {
		results[it.idx] = it.res
	}

	// Append tool results in the same order as the assistant's tool calls so
	// provider APIs that expect call/result pairing stay deterministic.
	failures, inert := 0, 0
	for _, ev := range results {
		if ev.failed {
			failures++
		}
		if ev.inert {
			inert++
		}
		for _, m := range ev.followUps {
			l.Messages = append(l.Messages, m)
			l.persist(ctx, m)
		}
		if ev.fatal {
			out <- ErrorEvent{Err: ev.err}
			return false, failures, inert
		}
	}
	return true, failures, inert
}

// invoke runs a single tool call, emits the matching events, and
// returns the messages to append to history.
func (l *Loop) invoke(ctx context.Context, tc llm.ToolCall, out chan<- Event) toolResult {
	// Wave 1 hardening for small models: validate the tool name
	// (with a did-you-mean suggestion) and repair truncated or
	// unbalanced JSON arguments before execution. An unrepairable
	// call is bounced back to the model with the correct format so
	// it can retry (up to maxToolFormatRetries consecutive times).
	if errMsg := HardenToolCall(&tc, l.registry.Names(), l.recentBadCallStreak()); errMsg != "" {
		out <- ToolCallEvent{ID: tc.ID, Name: tc.Name, Args: tc.Arguments}
		out <- ToolResultEvent{ID: tc.ID, Err: fmt.Errorf("%s", errMsg)}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    errMsg,
			}},
		}
	}

	raw := json.RawMessage(tc.Arguments)
	// Announce the call before execution. Long-running tools (most notably a
	// synchronous task worker) must become visible to TUI/WebGUI while they are
	// running, not only after Execute returns. ToolResultEvent remains the
	// matching completion edge, so clients can measure and render real elapsed
	// time without a separate worker-specific protocol.
	out <- ToolCallEvent{ID: tc.ID, Name: tc.Name, Args: tc.Arguments}

	// Two identical failures already taught the model; block a third
	// identical attempt before Execute (no extra LLM call).
	if l.identicalFails.shouldBlock(tc.Name, tc.Arguments) {
		msg := fmt.Sprintf(
			"blocked: tool %q with these exact arguments already failed twice in this run; change arguments or strategy (do not retry identically)",
			tc.Name,
		)
		out <- ToolResultEvent{ID: tc.ID, Err: fmt.Errorf("%s", msg)}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    msg,
			}},
		}
	}

	// The mirror case, and the one that cost a whole session: the identical
	// edit keeps SUCCEEDING. Nothing pushes back on a write that works, so
	// the same patch was applied 23 times. Repeats of a read are fine and
	// never reach here; only mutations are counted.
	if l.identicalWrites.shouldBlock(tc.Name, tc.Arguments) {
		msg := fmt.Sprintf(
			"blocked: tool %q with these exact arguments already succeeded %d times in this run; the edit is already applied — verify the file instead of repeating it",
			tc.Name, repeatedMutationLimit,
		)
		out <- ToolResultEvent{ID: tc.ID, Err: fmt.Errorf("%s", msg)}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    msg,
			}},
		}
	}

	// Per-tool timing under a "tool:<name>" phase key. Repeated calls
	// of the same tool in one step accumulate; the recorder is
	// mutex-guarded so parallel task batches are safe. Goes straight
	// to the recorder (NOT the wall-phase sum): the whole batch is
	// already covered by tool_execution in run().
	execStart := time.Now()
	var res tools.Result
	var err error
	if isPassingGoalVerification(tc.Name, raw) && (!l.toolEvidence.Load() || l.concreteFailure.Load()) {
		res.Err = fmt.Errorf("goal: passing verification requires a successful concrete check after the latest tool failure")
	} else if isGoalTaskCompletion(tc.Name, raw) && l.concreteFailure.Load() {
		res.Err = fmt.Errorf("goal: cannot complete a task after a failed tool result; fix the failure and run a successful concrete check first")
	} else {
		res, err = l.registry.Execute(ctx, tc.Name, raw)
	}
	l.recordPhase("tool:"+tc.Name, time.Since(execStart))

	// F4.e: tool result verification. After a successful
	// (no Go-level error) execution, ask the per-tool
	// override or the DefaultVerifier whether the result
	// is trustworthy. A failure rewrites res to surface
	// the reason instead of the (potentially lying)
	// tool output. Record identical-failure AFTER this so
	// a verifier rewrite still counts as a failed attempt.
	if err == nil {
		tool, ok := l.registry.Get(tc.Name)
		if ok {
			res = tools.ApplyVerification(tools.Check{
				Tool:    tc.Name,
				Args:    raw,
				Result:  res,
				BaseDir: l.baseDir,
			}, tool.Verify)
		}
	}
	if err != nil || res.Err != nil {
		l.identicalFails.recordFailure(tc.Name, tc.Arguments)
	} else {
		l.identicalFails.recordSuccess(tc.Name, tc.Arguments)
		l.identicalWrites.recordSuccess(tc.Name, tc.Arguments)
	}

	// F4.d: classify any error/verification failure and
	// log it to the error log if configured. We log
	// after the verification rewrite so the log records
	// the reason the model actually sees.
	if l.errorLog != nil {
		cat := tools.Classifier{}.Classify(tc.Name, raw, res)
		if cat.Category != tools.CategoryUnknown {
			l.errorLog.Append(tools.ErrorRecord{
				Tool:       tc.Name,
				Args:       string(raw),
				Category:   cat.Category.String(),
				Confidence: cat.Confidence,
				Reason:     cat.Reason,
				Suggestion: cat.Suggestion,
			})
		}
	}

	// Tool not found.
	if err != nil {
		if isConcreteEvidenceTool(tc.Name) {
			l.concreteFailure.Store(true)
		}
		out <- ToolResultEvent{ID: tc.ID, Err: err}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    "error: " + err.Error(),
			}},
		}
	}
	if res.Err != nil {
		if isConcreteEvidenceTool(tc.Name) {
			l.concreteFailure.Store(true)
		}
		out <- ToolResultEvent{ID: tc.ID, Output: res.Text, Err: res.Err}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				// ModelContent is the single contract point for
				// what the model sees: the error PLUS a capped
				// tail of res.Text (deduplicated), so diagnostics
				// a tool returns next to its error are not lost.
				Content: res.ModelContent(),
			}},
		}
	}
	if isConcreteEvidenceTool(tc.Name) {
		l.toolEvidence.Store(true)
		l.concreteFailure.Store(false)
	}

	out <- ToolResultEvent{ID: tc.ID, Output: res.Text}
	modelContent := l.registry.CompactModelOutput(tc.Name, res.ModelContent())
	follow := []llm.Message{{
		Role:       llm.RoleTool,
		ToolCallID: tc.ID,
		Name:       tc.Name,
		Content:    modelContent,
	}}
	if res.Image != nil {
		img := &llm.ImageRef{
			MediaType: res.Image.MediaType,
			Data:      base64.StdEncoding.EncodeToString(res.Image.Data),
		}
		follow = append(follow, llm.Message{
			Role: llm.RoleUser,
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "Attached image from tool " + tc.Name + ":"},
				{Type: llm.PartTypeImage, Image: img},
			},
		})
	}
	return toolResult{followUps: follow, inert: res.Inert}
}

func isPassingGoalVerification(name string, raw json.RawMessage) bool {
	if name != "goal" {
		return false
	}
	var p struct {
		Action string `json:"action"`
		Passed bool   `json:"passed"`
	}
	return json.Unmarshal(raw, &p) == nil && p.Action == "verify" && p.Passed
}

func isGoalTaskCompletion(name string, raw json.RawMessage) bool {
	if name != "goal" {
		return false
	}
	var p struct {
		Action string `json:"action"`
	}
	return json.Unmarshal(raw, &p) == nil && p.Action == "complete_task"
}

func isConcreteEvidenceTool(name string) bool {
	switch name {
	case "", "goal", "tool_search", "recall", "remember", "ask_user", "send_message", "task_stop":
		return false
	default:
		return true
	}
}

// sisyphusHitFromMessage extracts the 1-indexed attempt
// counter from a Sisyphus reminder so the TUI can render
// "Sisyphus #1/3" etc. The reminder format is
// "[Sisyphus @N/M] ..."; we parse N out of the bracket.
// If parsing fails, return 1 so the UI never shows "0".
func sisyphusHitFromMessage(msg string) int {
	const tag = "[Sisyphus @"
	i := strings.Index(msg, tag)
	if i < 0 {
		return 1
	}
	rest := msg[i+len(tag):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return 1
	}
	n := 0
	for _, ch := range rest[:slash] {
		if ch < '0' || ch > '9' {
			return 1
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return 1
	}
	return n
}
