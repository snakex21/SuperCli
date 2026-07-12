// Package tools implements the tool registry. F1 adds the ability
// to actually execute registered tools; F4 adds visibility (so the
// model sees only a filtered subset), tool search, and user-tool
// loading.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Result is what a tool returns after execution. Exactly one of
// Text or Image is usually populated; both is fine. Err is the
// error from the tool itself (file not found, permission denied,
// ...), distinct from a Go-level error.
type Result struct {
	Text  string
	Image *ImageContent
	Err   error
}

// ImageContent holds an image produced by a tool. The agent loop
// converts it to an llm.ImageRef before sending to the provider.
type ImageContent struct {
	MediaType string
	Data      []byte // raw bytes (will be base64 encoded)
}

// Tool is the description of a tool plus its execution function.
// The Fn field is mandatory for an executable tool; the registry
// refuses to register a tool without one.
type Tool struct {
	Name        string
	Description string
	// ReadOnly certifies that concurrent execution cannot mutate files,
	// process state, user data, or external services. The agent may run a
	// batch consisting exclusively of ReadOnly tools in parallel. False is
	// the safe default; tools must opt in explicitly.
	ReadOnly bool
	// Schema is a JSON Schema string for the tool's arguments.
	// F4 will parse and validate against it. For F1 it is passed
	// through to the provider as a hint.
	Schema string
	// Fn executes the tool. args is the raw JSON the model sent.
	// The function returns a Result whose Err field is the
	// user-visible tool error; the second return is for Go-level
	// exceptions (panic recovery, ...).
	Fn func(ctx context.Context, args json.RawMessage) (Result, error)
	// Verify is the F4.e optional per-tool verification hook.
	// When set, the agent loop calls Verify on the tool's
	// Result before returning it to the model. When nil, the
	// DefaultVerifier applies family heuristics based on the
	// tool name and result shape.
	Verify func(Result) VerifyVerdict
}

// Validate returns nil if the tool has the minimum required fields
// to be registered.
func (t Tool) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("tool: name is empty")
	}
	if t.Description == "" {
		return fmt.Errorf("tool %q: description is empty", t.Name)
	}
	if t.Fn == nil {
		return fmt.Errorf("tool %q: Fn is nil", t.Name)
	}
	return nil
}

// Registry stores tools by name. It is safe for concurrent use
// (sync.RWMutex). F4 adds visibility: tools can be registered
// up-front but only "activated" tools are exposed to the model.
// Activate / Deactivate control what the model sees at any given
// turn; MarkAlwaysOn / ResetVisibility manage the meta set.
type Registry struct {
	mu       sync.RWMutex
	tools    map[string]Tool
	visible  map[string]struct{} // subset of tools visible to the model
	order    []string            // insertion order, for stable Visible()
	alwaysOn map[string]struct{} // tools that ignore visibility (tool_search, ask_user, read_image, ...)
}

// NewRegistry returns an empty registry. Nothing is visible
// until Activate or MarkAlwaysOn is called.
func NewRegistry() *Registry {
	return &Registry{
		tools:    make(map[string]Tool),
		visible:  make(map[string]struct{}),
		alwaysOn: make(map[string]struct{}),
	}
}

// Register adds t. If a tool with the same name exists, Register
// returns an error and the registry is unchanged. The newly
// registered tool is NOT visible until Activate is called.
func (r *Registry) Register(t Tool) error {
	if err := t.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; exists {
		return fmt.Errorf("register %q: already present", t.Name)
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
	return nil
}

// MustRegister is the panicking variant for init() and tests.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// MarkAlwaysOn flags a tool as always visible regardless of
// the visible set. Useful for meta-tools (tool_search,
// ask_user) and image tools (read_image) that the model needs
// in every turn. Must be called AFTER Register. Idempotent.
func (r *Registry) MarkAlwaysOn(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alwaysOn[name] = struct{}{}
}

// Activate marks the named tools as visible to the model.
// Unknown names are silently ignored. The set is cumulative;
// call ResetVisibility to clear.
func (r *Registry) Activate(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		if _, ok := r.tools[n]; ok {
			r.visible[n] = struct{}{}
		}
	}
}

// Deactivate removes the named tools from the visible set.
// Always-on tools are NOT removed; this only affects the
// visibility flags. Use ResetVisibility to clear them all at
// once.
func (r *Registry) Deactivate(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		delete(r.visible, n)
	}
}

// ResetVisibility clears the visibility set. Always-on tools
// remain visible because they bypass the set.
func (r *Registry) ResetVisibility() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.visible = make(map[string]struct{})
}

// Get returns the tool and a found flag.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names returns the registered tool names in unspecified order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	return out
}

// Len reports the number of registered tools.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Visible returns the tools the model should see right now:
// union of the visible set and the always-on tools, in
// insertion order. Useful for building the provider's tool
// list.
func (r *Registry) Visible() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.visible)+len(r.alwaysOn))
	for _, n := range r.order {
		if _, ok := r.visible[n]; ok {
			out = append(out, r.tools[n])
			continue
		}
		if _, ok := r.alwaysOn[n]; ok {
			out = append(out, r.tools[n])
		}
	}
	return out
}

// VisibleNames returns the names of currently-visible tools,
// in insertion order. Useful for logging and tests.
func (r *Registry) VisibleNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.visible)+len(r.alwaysOn))
	for _, n := range r.order {
		if _, ok := r.visible[n]; ok {
			out = append(out, n)
			continue
		}
		if _, ok := r.alwaysOn[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// ActiveNames returns the names of tools that were explicitly
// activated (via Activate / tool_search), in insertion order.
// Unlike VisibleNames it EXCLUDES always-on tools — it answers
// "what did the model pull in on demand?", which the thin tool
// protocol needs to distinguish an activated tail tool (gets a
// full schema) from a dormant one (advertised in the catalog).
func (r *Registry) ActiveNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.visible))
	for _, n := range r.order {
		if _, ok := r.visible[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// IsVisible reports whether a tool is currently visible.
func (r *Registry) IsVisible(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.alwaysOn[name]; ok {
		return true
	}
	_, ok := r.visible[name]
	return ok
}

// Execute looks up the tool by name and invokes it. Unknown tools
// return ErrUnknownTool. Visibility is NOT checked here: the
// agent loop is responsible for filtering what the model can
// call; once a tool is registered, Execute accepts it. This
// keeps the safety boundary explicit (one place: the loop).
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return Result{Err: fmt.Errorf("%w: %q", ErrUnknownTool, name)}, ErrUnknownTool
	}
	// Lenient arg coercion for small/local models that stringify
	// scalars ({"limit":"5"} -> {"limit":5}). Schema-driven and
	// conservative: only fields the tool's schema types as
	// int/number/bool are touched, and only when the string is a
	// valid literal. See coerce_args.go.
	args = CoerceArgs(t.Schema, args)
	return t.Fn(ctx, args)
}

// ErrUnknownTool is returned by Execute when the name is not in
// the registry. Use errors.Is(err, tools.ErrUnknownTool) to detect.
var ErrUnknownTool = fmt.Errorf("unknown tool")
