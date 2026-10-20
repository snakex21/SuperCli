package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"supercli/internal/llm"
)

const sourceEvidenceRows = 8192

type sourceLocation struct {
	path string
	line int
}

type sourceRow struct {
	location sourceLocation
	hash     [sha256.Size]byte
	start    int
	end      int
}

// Interpret only registered source-reader framing, never user/assistant text.
// Evidence is the actual returned line, not the regex or requested range.
func sourceRows(call llm.ToolCall, content, baseDir string) []sourceRow {
	return sourceRowsFiltered(call, content, baseDir, nil)
}

// sourceRowsFiltered retains the same framing and 2000-row inspection budget as
// sourceRows. A worker may reject a path before hashing or materializing its rows.
// Rejected rows still consume the inspection budget; filtering must not expose
// evidence beyond the generic parser's original prefix.
func sourceRowsFiltered(call llm.ToolCall, content, baseDir string, include func(string) bool) []sourceRow {
	call = sourceCall(call)
	var file string
	switch call.Name {
	case "read_lines", "read_context":
		var args struct {
			File string `json:"file"`
		}
		if json.Unmarshal([]byte(call.Arguments), &args) != nil || args.File == "" {
			return nil
		}
		file = args.File
	case "read_many", "search_code", "read_output":
	default:
		return nil
	}
	if strings.HasPrefix(content, "error:") || strings.HasPrefix(content, pruneMarkerPrefix) {
		return nil
	}
	var rows []sourceRow
	type pathSelection struct {
		path    string
		include bool
	}
	var paths map[string]pathSelection
	if include != nil {
		paths = make(map[string]pathSelection)
	}
	inspected := 0
	for offset := 0; offset < len(content) && inspected < 2000; {
		end := strings.IndexByte(content[offset:], '\n')
		if end < 0 {
			end = len(content)
		} else {
			end += offset + 1
		}
		line := strings.TrimSuffix(strings.TrimSuffix(content[offset:end], "\n"), "\r")
		if path := sourceHeader(line); path != "" {
			file = path
		} else {
			path, number, text := file, 0, ""
			if prefix, body, ok := strings.Cut(line, " | "); ok && file != "" {
				number, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(prefix), ">")))
				text = body
			} else if call.Name == "search_code" {
				// The last :number: before the source body handles drive letters.
				for colon := strings.IndexByte(line, ':'); colon >= 0; {
					rest := line[colon+1:]
					next := strings.IndexByte(rest, ':')
					if next < 0 {
						break
					}
					if n, err := strconv.Atoi(rest[:next]); err == nil && n > 0 {
						path, number, text = line[:colon], n, rest[next+1:]
						break
					}
					colon += next + 1
				}
			}
			// An excerpt is not a complete source line. Keep it and its diagnostics.
			if number > 0 && path != "" && !strings.Contains(text, "truncated") &&
				!strings.Contains(text, "...") && !strings.Contains(text, "…") {
				inspected++
				selected := pathSelection{include: true}
				if include == nil {
					selected.path = sourcePath(path, baseDir)
				} else {
					var known bool
					selected, known = paths[path]
					if !known {
						selected.path = sourcePath(path, baseDir)
						selected.include = include(selected.path)
						paths[path] = selected
					}
				}
				if selected.include {
					rows = append(rows, sourceRow{
						location: sourceLocation{selected.path, number},
						hash:     sha256.Sum256([]byte(text)), start: offset, end: end,
					})
				}
			}
		}
		offset = end
	}
	return rows
}

func sourcePath(path, baseDir string) string {
	path = filepath.FromSlash(strings.ReplaceAll(path, "\\", "/"))
	if baseDir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

// A partial preview, mixed failure or stored-output chunk can contain fresh
// evidence we cannot classify. It may earn advice, but never a semantic stop.
func sourceResultPartial(call llm.ToolCall, content string, rows []sourceRow) bool {
	if sourceCall(call).Name == "read_output" || len(rows) >= 2000 {
		return true
	}
	text := strings.ToLower(content)
	for _, marker := range []string{"error:", "truncated", "omitted", "limit reached", "...", "…", "selected matching lines", "not retained:"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func sourceHeader(line string) string {
	if !strings.HasPrefix(line, "== ") || !strings.HasSuffix(line, " ==") {
		return ""
	}
	body := strings.TrimSuffix(strings.TrimPrefix(line, "== "), " ==")
	if strings.HasPrefix(body, "[") {
		if _, rest, ok := strings.Cut(body, "] "); ok {
			body = rest
		} else {
			return ""
		}
	}
	if strings.HasPrefix(body, "\"") {
		path, err := strconv.Unquote(body)
		if err == nil {
			return path
		}
		return ""
	}
	colon := strings.LastIndexByte(body, ':')
	if colon < 1 {
		return ""
	}
	from, to, ok := strings.Cut(body[colon+1:], "-")
	a, e1 := strconv.Atoi(from)
	b, e2 := strconv.Atoi(to)
	if !ok || e1 != nil || e2 != nil || a < 1 || b < a {
		return ""
	}
	return body[:colon]
}

// Dispatcher resolution here is descriptive only: no activation or execution.
func sourceCall(call llm.ToolCall) llm.ToolCall {
	if call.Name != invokeToolName {
		return call
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(call.Arguments), &envelope) != nil {
		return call
	}
	var target string
	if json.Unmarshal(envelope["tool"], &target) != nil {
		return call
	}
	args := map[string]json.RawMessage{}
	if raw := envelope["args"]; len(raw) > 0 {
		var err error
		args, err = decodeInvokeArgs(raw)
		if err != nil {
			return call
		}
	}
	for key, value := range envelope {
		if field, ok := strings.CutPrefix(key, "arg."); ok {
			if _, exists := args[field]; exists {
				return call
			}
			args[field] = value
		}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return call
	}
	call.Name, call.Arguments = strings.TrimSpace(target), string(encoded)
	return call
}

type visibleSource struct {
	hash [sha256.Size]byte
	call string
}

const sourceFileListingEntries = 64

type sourceFileListingKey struct {
	root string
	hash [sha256.Size]byte
}

type visibleFileListing struct {
	content string
	call    string
}

// Only complete, plain path listings qualify. Diagnostics, previews and source
// excerpts remain verbatim. This does not cache execution: every search still
// runs, and equality is checked against its actual returned bytes.
func sourceFileListing(call llm.ToolCall, message llm.Message, baseDir string) (sourceFileListingKey, bool) {
	if call.Name != "search_code" || len(message.Parts) != 0 || len(message.ToolCalls) != 0 || len(message.Content) < 512 || len(message.Content) > 64*1024 {
		return sourceFileListingKey{}, false
	}
	var args struct {
		Query      string `json:"query"`
		Path       string `json:"path"`
		OutputMode string `json:"output_mode"`
	}
	if json.Unmarshal([]byte(call.Arguments), &args) != nil || args.Query == "" || args.OutputMode != "files" {
		return sourceFileListingKey{}, false
	}
	for _, line := range strings.Split(strings.TrimSuffix(message.Content, "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line != strings.TrimSpace(line) || !strings.ContainsAny(line, `/\`) ||
			strings.ContainsAny(line, "[]<>|\t\x00") || strings.Contains(line, ": ") || strings.HasPrefix(line, "...") {
			return sourceFileListingKey{}, false
		}
	}
	return sourceFileListingKey{root: sourcePath(args.Path, baseDir), hash: sha256.Sum256([]byte(message.Content))}, true
}

// Rebuild from the exact visible request, so compacted, hidden or pruned reads
// never justify a reference to missing code. The canonical transcript is intact.
// Deterministic forward processing keeps earlier request bytes stable on append.
func sourceEvidenceProjection(messages []llm.Message, baseDir string) []llm.Message {
	var projected []llm.Message
	seen := make(map[sourceLocation]visibleSource)
	var fileLists map[sourceFileListingKey]visibleFileListing
	var calls map[string]llm.ToolCall
	for index, message := range messages {
		if message.Role == llm.RoleAssistant {
			calls = nil
			for _, call := range message.ToolCalls {
				if calls == nil {
					calls = make(map[string]llm.ToolCall)
				}
				calls[call.ID] = call
			}
		}
		if message.Role != llm.RoleTool {
			continue
		}
		call, found := calls[message.ToolCallID]
		if !found || message.ToolCallID == "" {
			continue
		}
		call = sourceCall(call)
		if key, ok := sourceFileListing(call, message, baseDir); ok {
			prior, repeated := fileLists[key]
			marker := fmt.Sprintf("[Identical complete file list; full output already shown in tool result %s]\n", prior.call)
			if repeated && prior.content == message.Content && len(message.Content)-len(marker) >= 128 {
				if projected == nil {
					projected = append([]llm.Message(nil), messages...)
				}
				projected[index].Content = marker
			} else {
				if fileLists == nil {
					fileLists = make(map[sourceFileListingKey]visibleFileListing)
				} else if len(fileLists) >= sourceFileListingEntries {
					clear(fileLists)
				}
				fileLists[key] = visibleFileListing{content: message.Content, call: message.ToolCallID}
			}
			continue
		}
		rows := sourceRows(call, message.Content, baseDir)
		// A targeted read is an explicit request for source, not discovery. A
		// read_output recovery must likewise return the saved bytes. Keep both
		// intact, including through invoke_tool, and let later discovery refer
		// to this latest complete evidence instead of the earlier search.
		if call.Name == "read_lines" || call.Name == "read_output" {
			for _, row := range rows {
				if len(seen) >= sourceEvidenceRows {
					clear(seen)
				}
				seen[row.location] = visibleSource{row.hash, message.ToolCallID}
			}
			continue
		}
		var body strings.Builder
		last := 0
		for i := 0; i < len(rows); {
			row := rows[i]
			prior, repeated := seen[row.location]
			repeated = repeated && prior.hash == row.hash
			j := i + 1
			if repeated {
				for j < len(rows) {
					next := rows[j]
					old, exists := seen[next.location]
					if !exists || old.hash != next.hash || old.call != prior.call ||
						next.location.path != row.location.path || next.location.line != rows[j-1].location.line+1 ||
						next.start != rows[j-1].end {
						break
					}
					j++
				}
				marker := fmt.Sprintf("[Unchanged source lines %d-%d; already shown in tool result %s]\n", row.location.line, rows[j-1].location.line, prior.call)
				if rows[j-1].end-row.start-len(marker) >= 128 {
					body.WriteString(message.Content[last:row.start])
					body.WriteString(marker)
					last = rows[j-1].end
				}
			}
			for _, fresh := range rows[i:j] {
				old, exists := seen[fresh.location]
				if !exists || old.hash != fresh.hash {
					if len(seen) >= sourceEvidenceRows {
						clear(seen)
					}
					seen[fresh.location] = visibleSource{fresh.hash, message.ToolCallID}
				}
			}
			i = j
		}
		if last > 0 {
			body.WriteString(message.Content[last:])
			if projected == nil {
				projected = append([]llm.Message(nil), messages...)
			}
			projected[index].Content = body.String()
		}
	}
	if projected == nil {
		return messages
	}
	return projected
}
