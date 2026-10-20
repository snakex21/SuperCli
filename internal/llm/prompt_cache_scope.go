package llm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
)

type promptCacheScopeKey struct{}

// WithPromptCacheScope separates independent continuations without changing
// the conversation ID used for history, billing, cancellation or reasoning.
// Callers use one unique owning-loop identity across its invocations. Replace
// inherited scope so a nested worker resumed by its coordinator keeps its key.
func WithPromptCacheScope(ctx context.Context, scope string) context.Context {
	if ctx == nil || strings.TrimSpace(scope) == "" {
		return ctx
	}
	return context.WithValue(ctx, promptCacheScopeKey{}, scope)
}

// Only transports supporting cache routing consume this ID. Do not invent
// request fields for servers with implicit prefix caching (Anthropic) or only
// cache_prompt support (chat/llama.cpp). A stable key is a routing hint, not a
// guarantee that the backend retains a prefix or reports a cache hit.
func promptCacheSessionID(ctx context.Context, fallback string) string {
	base := openCodeSessionFromContext(ctx)
	if base == "" {
		base = fallback
	}
	var scope string
	if ctx != nil {
		scope, _ = ctx.Value(promptCacheScopeKey{}).(string)
	}
	purpose := PurposeFromContext(ctx)
	// Ordinary foreground calls keep their existing key. Worker identity is
	// enough to separate task/advisor continuations from their parent; helper
	// purposes also need a separate prefix from that worker's own conversation.
	switch purpose {
	case "", PurposeMain, PurposeTask, PurposeAdvisor:
		purpose = ""
	}
	if scope == "" && purpose == "" {
		return base
	}
	sum := sha256.Sum256([]byte(base + "\x00" + scope + "\x00" + purpose))
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
