// Package draft implements F11: draft-model bridging. A
// "draft" is a small, cheap model (e.g. claude-haiku-4-5
// or gpt-4o-mini) that proposes a brief plan for the
// user's last message; the "verifier" (the main model)
// sees the draft as a system-message hint and may echo,
// shorten, or override it. When the verifier echoes /
// shortens, the draft's output tokens count as "saved"
// — those tokens are what the verifier would otherwise
// have spent producing its own plan.
//
// The package exposes three pieces:
//
//   - Policy (policy.go): the immutable configuration
//     (mode, task name, draft model id, the model
//     to exclude).
//   - Bridge (bridge.go): the runner that calls the
//     draft model and produces a BridgeResult the
//     loop can inject into the verifier's view.
//   - Savings (savings.go): a per-session counter
//     that computes the savings number the TUI shows
//     in its [draft: ...] marker.
//
// Dependency rules: draft depends on llm only. It does
// NOT import agent, reflect, or credits. The agent
// loop is the orchestrator; this package is a pure
// helper it can call.
package draft
