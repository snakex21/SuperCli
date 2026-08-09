package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (p *CodexProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("Complete: no messages")
	}
	for i, m := range msgs {
		if err := m.Validate(); err != nil {
			return nil, fmt.Errorf("Complete: message %d: %w", i, err)
		}
	}
	// Known text-only metadata blocks image input before the request is sent;
	// unknown capability metadata remains optimistic.
	visionAttempt := p.caps.AllowsVisionAttempt(p.cfg.Model) && !p.imageRejected.Load()
	reqBody, err := buildCodexRequest(p.cfg.Model, msgs, tools, visionAttempt)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	var imageFallback []byte
	if visionAttempt && messagesContainImage(msgs) {
		imageFallback, err = buildCodexRequest(p.cfg.Model, msgs, tools, false)
		if err != nil {
			return nil, fmt.Errorf("build image fallback request: %w", err)
		}
	}
	if p.cfg.StandardResponsesAPI {
		reasoningModel := SupportsReasoningEffort(p.cfg.Model)
		reqBody, err = prepareStandardResponsesRequest(reqBody, p.cfg.PromptCacheKey, reasoningModel, p.sampling)
		if err != nil {
			return nil, fmt.Errorf("build standard responses request: %w", err)
		}
		if len(imageFallback) > 0 {
			imageFallback, err = prepareStandardResponsesRequest(imageFallback, p.cfg.PromptCacheKey, reasoningModel, p.sampling)
			if err != nil {
				return nil, fmt.Errorf("build standard image fallback request: %w", err)
			}
		}
	}

	out := make(chan Delta, 16)
	go func() {
		defer close(out)
		defer func() {
			if r := recover(); r != nil {
				select {
				case out <- Delta{Err: fmt.Errorf("provider panic: %v", r)}:
				default:
				}
			}
		}()
		// Derive a cancellable child context so the idle watchdog can
		// abort a stalled stream. cancel runs after the read completes.
		reqCtx, cancel := context.WithCancel(ctx)
		// notify surfaces non-terminal rate-limit/5xx retry notices to
		// the UI (same Delta.Notice channel openai.go/anthropic.go use);
		// it respects ctx so a cancelled run doesn't block on the send.
		notify := func(d Delta) {
			select {
			case out <- d:
			case <-ctx.Done():
			}
		}
		resp, err := p.doWithAuth(reqCtx, cancel, reqBody, imageFallback, notify)
		if err != nil {
			cancel()
			select {
			case out <- Delta{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		defer cancel()
		body := newIdleTimeoutReader(resp.Body, p.cfg.Timeout, cancel)
		defer body.Close()
		p.streamCodexSSE(ctx, body, out)
	}()
	return out, nil
}

// doWithAuth performs the POST with bearer + ChatGPT headers.
// Four orthogonal retry paths interleave in one loop:
//   - 401: transparent token refresh (once);
//   - 400 effort-learn: patch reasoning effort from the error (once);
//   - explicit image-input rejection: retry without images and remember it (once);
//   - 429/5xx: honour Retry-After (capped by rateLimitWaitBudget),
//     mirroring openai.go/anthropic.go. Each rate-limit retry emits a
//     non-terminal Delta.Notice via notify (may be nil), and after the
//     retries are exhausted the terminal error carries the same
//     "switch model" hint as the other providers.
func (p *CodexProvider) doWithAuth(ctx context.Context, cancel context.CancelFunc, body, imageFallback []byte, notify func(Delta)) (*http.Response, error) {
	access, accountID, err := p.cfg.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	effortRetried := false
	refreshed := false
	visionRetried := false
	const maxRateLimitAttempts = 3
	rlAttempt := 0
	waitBudget := rateLimitWaitBudget
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			p.cfg.BackendURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if access != "" {
			req.Header.Set("Authorization", "Bearer "+access)
		}
		if !p.cfg.StandardResponsesAPI {
			req.Header.Set("OpenAI-Beta", "responses=experimental")
			req.Header.Set("originator", "codex_cli_go")
			if accountID != "" {
				req.Header.Set("chatgpt-account-id", accountID)
			}
		}
		resp, err := doWithResponseHeaderTimeout(p.http, req, p.cfg.Timeout, cancel)
		if err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}
		if resp.StatusCode/100 == 2 {
			// The ChatGPT backend stamps usage limits on the headers
			// of every 200 — refresh the HUD snapshot here (no extra
			// request). A non-Codex/empty header set yields OK=false
			// and is ignored by setRateLimits.
			p.setRateLimits(accountID, parseCodexRateLimits(resp.Header))
			return resp, nil
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && !refreshed && !p.cfg.StandardResponsesAPI {
			// Transparent refresh + single retry.
			refreshed = true
			access, err = p.cfg.Tokens.Refresh(ctx)
			if err != nil {
				return nil, fmt.Errorf("codex auth expired and refresh failed: %w", err)
			}
			continue
		}
		if effort, ok := LearnReasoningEffortFromError(p.cfg.Model, resp.StatusCode, respBody); ok && !effortRetried {
			if patched, patchedOK := patchCodexReasoningEffort(body, effort); patchedOK {
				body = patched
				effortRetried = true
				continue
			}
		}
		if !visionRetried && len(imageFallback) > 0 && isImageRejection(resp.StatusCode, respBody) {
			p.imageRejected.Store(true)
			body = imageFallback
			imageFallback = nil
			visionRetried = true
			if notify != nil {
				notify(Delta{Notice: fmt.Sprintf("model %q rejected image input; retrying without images", p.cfg.Model)})
			}
			continue
		}
		if isRetryableHTTPStatus(resp.StatusCode) && rlAttempt < maxRateLimitAttempts-1 {
			rlAttempt++
			wait := retryWait(resp.Header, rlAttempt, waitBudget)
			waitBudget -= wait
			if notify != nil {
				notify(Delta{Notice: rateLimitNotice(p.cfg.Model, resp.StatusCode, wait, rlAttempt, maxRateLimitAttempts)})
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return nil, fmt.Errorf("http %d: %s%s", resp.StatusCode, string(respBody), providerErrorHint(p.cfg.BackendURL, p.cfg.Model, resp.StatusCode, respBody))
	}
}

// streamCodexSSE parses Responses API SSE events into Deltas.
func (p *CodexProvider) streamCodexSSE(ctx context.Context, r io.Reader, out chan<- Delta) {
	emit := func(d Delta) bool {
		select {
		case out <- d:
			return true
		case <-ctx.Done():
			return false
		}
	}
	var usage *Usage
	reasoningOpen := false
	sentRole := false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || isDone(data) {
			continue
		}
		if isSSEHeartbeatData(data) {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			emit(Delta{Err: fmt.Errorf("%s: malformed SSE payload: %w", p.responsesAPIName(), err)})
			return
		}
		switch ev.Type {
		case "response.output_text.delta":
			if !sentRole {
				if !emit(Delta{Role: RoleAssistant}) {
					return
				}
				sentRole = true
			}
			content := ev.Delta
			if reasoningOpen {
				content = "</thinking>\n" + strings.TrimLeft(content, "\r\n")
				reasoningOpen = false
			}
			if !emit(Delta{Content: content}) {
				return
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			content := ev.Delta
			if !reasoningOpen {
				content = "<thinking>" + content
				reasoningOpen = true
			}
			if !emit(Delta{Content: content}) {
				return
			}
		case "response.output_item.done":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				if !emit(Delta{ToolCall: &ToolCall{
					ID:        ev.Item.CallID,
					Name:      ev.Item.Name,
					Arguments: ev.Item.Arguments,
				}}) {
					return
				}
			}
		case "response.completed":
			if ev.Response != nil && ev.Response.Usage != nil {
				usage = &Usage{
					Input:  ev.Response.Usage.InputTokens,
					Output: ev.Response.Usage.OutputTokens,
					Total:  ev.Response.Usage.TotalTokens,
				}
				if d := ev.Response.Usage.InputTokensDetails; d != nil {
					usage.CachedInput = d.CachedTokens
				}
				if d := ev.Response.Usage.OutputTokensDetails; d != nil {
					usage.Reasoning = d.ReasoningTokens
				}
			}
			if reasoningOpen {
				if !emit(Delta{Content: "</thinking>"}) {
					return
				}
				reasoningOpen = false
			}
			d := Delta{FinishReason: "stop"}
			if usage != nil {
				d.Usage = usage
			}
			emit(d)
			return
		case "response.failed":
			msg := "response failed"
			if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			emit(Delta{Err: fmt.Errorf("%s: %s", p.responsesAPIName(), msg)})
			return
		case "error":
			msg := ev.Message
			if msg == "" && ev.Error != nil {
				msg = ev.Error.Message
			}
			if msg == "" {
				msg = "stream error"
			}
			emit(Delta{Err: fmt.Errorf("%s: %s", p.responsesAPIName(), msg)})
			return
		}
	}
	if err := scanner.Err(); err != nil {
		emit(Delta{Err: fmt.Errorf("sse: %w", err)})
		return
	}
	emit(Delta{Err: fmt.Errorf("%s: stream ended before response.completed", p.responsesAPIName())})
}
