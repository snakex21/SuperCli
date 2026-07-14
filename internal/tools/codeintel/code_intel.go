// Package codeintel exposes a single, lazy Language Server Protocol tool.
// The LSP transport is kept behind one compact action schema so dormant code
// intelligence adds no provider prompt cost when thin tool discovery is used.
package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"supercli/internal/codeintel/lsp"
	"supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

const (
	maxSourceBytes = 4 << 20
	maxResults     = 50
	defaultTimeout = 10 * time.Second
)

// Tool owns the workspace-scoped lazy LSP manager. One manager is intended to
// live for the whole CLI/WebGUI engine lifetime so gopls/pyright/rust-analyzer
// is paid for once and reused across model turns.
type Tool struct {
	BaseDir string
	Manager *lsp.Manager
	// warmCheck is injected only by unit tests. Production calls
	// CheckIfWarm directly and never starts a server from a write hook.
	warmCheck func(context.Context, string) string
}

func New(baseDir string) *Tool {
	return &Tool{BaseDir: baseDir, Manager: lsp.NewManager(baseDir)}
}

func (t *Tool) Spec() core.Tool {
	return core.Tool{
		Name:        "code_intel",
		Description: "Use an installed language server for exact code diagnostics and navigation. Actions: diagnostics, outline, definition, references, implementation, symbols, servers. The server starts lazily and is reused; unsupported languages or missing binaries degrade cleanly. Prefer outline over reading a large source file and navigation over repeated text searches.",
		Schema:      `{"type":"object","properties":{"action":{"type":"string","enum":["diagnostics","outline","definition","references","implementation","symbols","servers"]},"path":{"type":"string","description":"Workspace-relative source file used as the language/position anchor"},"line":{"type":"integer","minimum":1},"column":{"type":"integer","minimum":1},"query":{"type":"string","description":"Symbol query for action=symbols"},"include_declaration":{"type":"boolean","default":true}},"required":["action"]}`,
		Fn:          t.Execute,
	}
}

type params struct {
	Action             string `json:"action"`
	Path               string `json:"path"`
	Line               int    `json:"line"`
	Column             int    `json:"column"`
	Query              string `json:"query"`
	IncludeDeclaration *bool  `json:"include_declaration"`
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	var p params
	if err := json.Unmarshal(raw, &p); err != nil {
		return core.Result{Err: fmt.Errorf("code_intel: bad args: %w", err)}, nil
	}
	p.Action = strings.ToLower(strings.TrimSpace(p.Action))
	if p.Action == "servers" {
		return core.Result{Text: serverStatus()}, nil
	}
	if t.Manager == nil {
		return core.Result{Err: fmt.Errorf("code_intel: manager unavailable")}, nil
	}
	if strings.TrimSpace(p.Path) == "" {
		return core.Result{Err: fmt.Errorf("code_intel: path is required for action %s", p.Action)}, nil
	}
	path, err := sandbox.ResolveSafe(t.BaseDir, p.Path)
	if err != nil {
		return core.Result{Err: fmt.Errorf("code_intel: %w", err)}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return core.Result{Err: fmt.Errorf("code_intel: %w", err)}, nil
	}
	if !info.Mode().IsRegular() {
		return core.Result{Err: fmt.Errorf("code_intel: not a regular file: %s", p.Path)}, nil
	}
	if info.Size() > maxSourceBytes {
		return core.Result{Err: fmt.Errorf("code_intel: file too large (%d bytes; max %d)", info.Size(), maxSourceBytes)}, nil
	}
	if !lsp.Available(path) {
		cmd, configured := lsp.ServerFor(path)
		if !configured {
			return core.Result{Text: fmt.Sprintf("code_intel unsupported for %s", filepath.Ext(path))}, nil
		}
		return core.Result{Text: fmt.Sprintf("code_intel unavailable: install %s and put it on PATH", cmd[0])}, nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return core.Result{Err: fmt.Errorf("code_intel: read %s: %w", p.Path, err)}, nil
	}
	callCtx, cancel := boundedContext(ctx, defaultTimeout)
	defer cancel()

	switch p.Action {
	case "diagnostics":
		diags, err := t.Manager.Check(callCtx, path, string(text))
		if err != nil {
			return core.Result{Err: fmt.Errorf("code_intel diagnostics: %w", err)}, nil
		}
		if len(diags) == 0 {
			return core.Result{Text: "diagnostics: clean (no findings published)"}, nil
		}
		if len(diags) > maxResults {
			diags = diags[:maxResults]
		}
		return core.Result{Text: lsp.FormatDiagnostics(displayPath(t.BaseDir, path), diags)}, nil
	case "outline":
		_, symbols, ok, err := t.Manager.Navigate(callCtx, lsp.NavRequest{Op: lsp.NavDocumentSymbol, Path: path, Text: string(text)})
		if err != nil {
			return core.Result{Err: fmt.Errorf("code_intel outline: %w", err)}, nil
		}
		if !ok || len(symbols) == 0 {
			return core.Result{Text: "outline: no symbols"}, nil
		}
		return core.Result{Text: formatSymbols(t.BaseDir, symbols)}, nil
	case "definition", "references", "implementation":
		if p.Line < 1 || p.Column < 1 {
			return core.Result{Err: fmt.Errorf("code_intel: line and column (1-based) are required for %s", p.Action)}, nil
		}
		op := lsp.NavOp(p.Action)
		include := true
		if p.IncludeDeclaration != nil {
			include = *p.IncludeDeclaration
		}
		locations, _, ok, err := t.Manager.Navigate(callCtx, lsp.NavRequest{Op: op, Path: path, Line: p.Line, Character: p.Column, Text: string(text), IncludeDeclaration: include})
		if err != nil {
			return core.Result{Err: fmt.Errorf("code_intel %s: %w", p.Action, err)}, nil
		}
		if !ok || len(locations) == 0 {
			return core.Result{Text: p.Action + ": no locations"}, nil
		}
		return core.Result{Text: formatLocations(t.BaseDir, locations)}, nil
	case "symbols":
		if strings.TrimSpace(p.Query) == "" {
			return core.Result{Err: fmt.Errorf("code_intel: query is required for symbols")}, nil
		}
		_, symbols, ok, err := t.Manager.Navigate(callCtx, lsp.NavRequest{Op: lsp.NavWorkspaceSymbol, Path: path, Query: p.Query, Text: string(text)})
		if err != nil {
			return core.Result{Err: fmt.Errorf("code_intel symbols: %w", err)}, nil
		}
		if !ok || len(symbols) == 0 {
			return core.Result{Text: "symbols: no matches"}, nil
		}
		return core.Result{Text: formatSymbols(t.BaseDir, symbols)}, nil
	default:
		return core.Result{Err: fmt.Errorf("code_intel: unknown action %q", p.Action)}, nil
	}
}

// CheckIfWarm returns diagnostics only when this tool already has a live server
// for path. It never starts a process, which makes it safe to call after edits.
func (t *Tool) CheckIfWarm(ctx context.Context, path string) string {
	if t == nil {
		return ""
	}
	if t.warmCheck != nil {
		return t.warmCheck(ctx, path)
	}
	if t.Manager == nil {
		return ""
	}
	full, err := sandbox.ResolveSafe(t.BaseDir, path)
	if err != nil || !t.Manager.RunningFor(full) {
		return ""
	}
	data, err := os.ReadFile(full)
	if err != nil || len(data) > maxSourceBytes {
		return ""
	}
	callCtx, cancel := boundedContext(ctx, 1500*time.Millisecond)
	defer cancel()
	diags, err := t.Manager.Check(callCtx, full, string(data))
	if err != nil || len(diags) == 0 {
		return ""
	}
	if len(diags) > 12 {
		diags = diags[:12]
	}
	return lsp.FormatDiagnostics(displayPath(t.BaseDir, full), diags)
}

var mutationTools = map[string][]string{
	"edit_line":    {"path"},
	"edit_lines":   {"path"},
	"insert_after": {"path"},
	"delete_lines": {"path"},
	"write_file":   {"path"},
	"move":         {"dst", "src"},
	"copy":         {"dst"},
}

// WrapMutation appends diagnostics after a successful source-file mutation,
// but only if that language server was already running. The wrapper is carried
// with the Tool value into delegated worker registries, avoiding a second
// wiring mechanism and preserving the zero-startup-cost contract.
func (t *Tool) WrapMutation(spec core.Tool) core.Tool {
	fields, ok := mutationTools[spec.Name]
	if !ok || spec.Fn == nil {
		return spec
	}
	original := spec.Fn
	spec.Fn = func(ctx context.Context, args json.RawMessage) (core.Result, error) {
		result, err := original(ctx, args)
		if err != nil || result.Err != nil {
			return result, err
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(args, &values) != nil {
			return result, nil
		}
		for _, field := range fields {
			var path string
			if json.Unmarshal(values[field], &path) != nil || strings.TrimSpace(path) == "" {
				continue
			}
			if diagnostics := t.CheckIfWarm(ctx, path); diagnostics != "" {
				if result.Text != "" {
					result.Text += "\n\n"
				}
				result.Text += "[LSP diagnostics after edit]\n" + diagnostics
				break
			}
		}
		return result, nil
	}
	return spec
}

func (t *Tool) Close() {
	if t == nil || t.Manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = t.Manager.Shutdown(ctx)
}

func boundedContext(parent context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= limit {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, limit)
}

func serverStatus() string {
	var lines []string
	for _, binary := range lsp.ServerBinaries() {
		_, err := exec.LookPath(binary)
		state := "missing"
		if err == nil {
			state = "ready"
		}
		lines = append(lines, binary+": "+state)
	}
	return strings.Join(lines, "\n")
}

func formatLocations(baseDir string, locations []lsp.Location) string {
	if len(locations) > maxResults {
		locations = locations[:maxResults]
	}
	lines := make([]string, 0, len(locations))
	for _, loc := range locations {
		path := displayPath(baseDir, lsp.URIToPath(loc.URI))
		lines = append(lines, fmt.Sprintf("%s:%d:%d", path, loc.Range.Start.Line+1, loc.Range.Start.Character+1))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func formatSymbols(baseDir string, symbols []lsp.SymbolResult) string {
	if len(symbols) > maxResults {
		symbols = symbols[:maxResults]
	}
	lines := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		path := displayPath(baseDir, lsp.URIToPath(symbol.Location.URI))
		indent := strings.Repeat("  ", symbol.Depth)
		detail := ""
		if strings.TrimSpace(symbol.Detail) != "" {
			detail = " " + strings.TrimSpace(symbol.Detail)
		}
		lines = append(lines, fmt.Sprintf("%s%s %s%s %s:%d:%d", indent, symbol.Kind, symbol.Name, detail, path, symbol.Location.Range.Start.Line+1, symbol.Location.Range.Start.Character+1))
	}
	if len(symbols) > 0 && !symbols[0].Outline {
		sort.Strings(lines)
	}
	return strings.Join(lines, "\n")
}

func displayPath(baseDir, path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	base, err := filepath.Abs(baseDir)
	if err == nil {
		if rel, relErr := filepath.Rel(base, abs); relErr == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return rel
		}
	}
	return filepath.Clean(abs)
}
