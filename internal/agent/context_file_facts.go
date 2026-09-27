package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

const compactFactsMaxBytes = 2048

type compactFileFact struct {
	path string
	kind int
	last int
}

const (
	compactReferenced = iota + 1
	compactRead
	compactModified
)

// CompactFacts preserves bounded, exact file facts from completed tool pairs.
// Calls alone describe intent. Pruned results retain references, not claims that
// the operation succeeded. This runs only at compaction and does no I/O.
func CompactFacts(msgs []llm.Message, loadedTools []string) string {
	facts := map[string]compactFileFact{}
	record := func(path string, kind, last int) {
		if strings.TrimSpace(path) == "" {
			return
		}
		key := normalizeToolPath(path)
		prior := facts[key]
		facts[key] = compactFileFact{path: path, kind: max(kind, prior.kind), last: last}
	}
	var pending map[string]llm.ToolCall
	for i, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			pending = nil
		case llm.RoleAssistant:
			pending = make(map[string]llm.ToolCall, len(m.ToolCalls))
			for _, call := range m.ToolCalls {
				if call.ID == "" {
					continue
				}
				if _, duplicate := pending[call.ID]; duplicate {
					pending[call.ID] = llm.ToolCall{} // ambiguous ID
				} else {
					pending[call.ID] = call
				}
			}
		case llm.RoleTool:
			call, found := pending[m.ToolCallID]
			delete(pending, m.ToolCallID)
			if !found || call.Name == "" {
				continue
			}
			name, args := compactCallArgs(call)
			if name == "" || (m.Name != "" && m.Name != call.Name && m.Name != name) {
				continue
			}
			body := strings.TrimSpace(pruneStructuredBody(m.Content, core.StoredOutputHandle(m.Content)))
			if strings.HasPrefix(body, "error:") || strings.HasPrefix(body, "blocked:") {
				continue
			}
			if name == "read_many" {
				for _, path := range compactBatchReads(body) {
					record(path, compactRead, i)
				}
				continue
			}
			kind := compactResultKind(name, args, body)
			get := func(key string) string {
				var s string
				_ = json.Unmarshal(args[key], &s)
				return s
			}
			switch name {
			case "read_lines", "read_context":
				path := get("file")
				if path == "" {
					path = get("path") // older transcripts
				}
				record(path, kind, i)
			case "read_image", "read_docx", "read_pdf", "read_xlsx", "read_zip",
				"patch_file", "create_file", "write_file", "edit_line", "insert_lines", "delete_lines",
				"edit_docx", "edit_xlsx", "trash":
				record(get("path"), kind, i)
			case "copy", "move":
				record(get("dest"), kind, i)
				if name == "move" {
					record(get("src"), kind, i)
				}
			}
		}
	}
	ordered := make([]compactFileFact, 0, len(facts))
	for _, fact := range facts {
		ordered = append(ordered, fact)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].last == ordered[j].last {
			return ordered[i].path < ordered[j].path
		}
		return ordered[i].last > ordered[j].last
	})
	var b strings.Builder
	writeList := func(label string, entries []string) {
		var kept []string
		size := len(label) + 3 // newline + ": "
		for _, entry := range entries {
			// Quote only ambiguous paths/names; never split a path or UTF-8 rune.
			if strings.ContainsAny(entry, ",\r\n\t\"") {
				entry = strconv.Quote(entry)
			}
			extra := len(entry)
			if len(kept) > 0 {
				extra += 2
			}
			if b.Len()+size+extra > compactFactsMaxBytes {
				continue
			}
			kept = append(kept, entry)
			size += extra
			if len(kept) == 20 {
				break
			}
		}
		if len(kept) > 0 {
			fmt.Fprintf(&b, "\n%s: %s", label, strings.Join(kept, ", "))
		}
	}
	// Preserve active tools and completed changes before lower-value references.
	writeList("loaded_tools", loadedTools)
	for _, group := range []struct {
		kind  int
		label string
	}{{compactModified, "files_modified"}, {compactRead, "files_read"}, {compactReferenced, "files_referenced"}} {
		var entries []string
		for _, fact := range ordered {
			if fact.kind == group.kind {
				entries = append(entries, fact.path)
			}
		}
		writeList(group.label, entries)
	}
	return b.String()
}

func compactCallArgs(call llm.ToolCall) (string, map[string]json.RawMessage) {
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(call.Arguments), &args) != nil {
		return "", nil
	}
	if call.Name != invokeToolName {
		return call.Name, args
	}
	var name string
	if json.Unmarshal(args["tool"], &name) != nil {
		return "", nil
	}
	args, err := decodeInvokeEnvelope(args)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(name), args
}

func compactResultKind(name string, args map[string]json.RawMessage, body string) int {
	var dryRun bool
	_ = json.Unmarshal(args["dry_run"], &dryRun)
	if body == "" || dryRun || strings.HasPrefix(body, pruneMarkerPrefix) || strings.HasPrefix(body, "Preview only:") {
		return compactReferenced
	}
	switch name {
	case "read_lines", "read_context", "read_image", "read_docx", "read_pdf", "read_xlsx", "read_zip":
		return compactRead
	case "patch_file":
		var path string
		_ = json.Unmarshal(args["path"], &path)
		// Inspect generated metadata, never a matching phrase in the patch body
		// or in a filename containing "changed=true".
		tail, ok := strings.CutPrefix(body, "Patched "+path+": replacements=")
		if ok {
			_, rest, _ := strings.Cut(tail, " ")
			if strings.HasPrefix(rest, "changed=true ") {
				return compactModified
			}
		}
	case "create_file", "write_file":
		var path string
		_ = json.Unmarshal(args["path"], &path)
		if strings.HasPrefix(body, "Created "+path+" (") || (name == "write_file" && strings.HasPrefix(body, "Overwrote "+path+" (")) {
			return compactModified
		}
	case "move", "copy", "trash":
		if strings.HasPrefix(body, "Moved ") || strings.HasPrefix(body, "Copied ") {
			return compactModified
		}
	case "edit_docx", "edit_xlsx":
		if strings.Contains(body, "Nothing was changed") || strings.Contains(body, "Original document was not changed") {
			return compactReferenced
		}
		if strings.Contains(body, "Backup of the original saved as ") || strings.Contains(body, "Backup saved as ") ||
			strings.HasPrefix(body, "Applied ") || (name == "edit_docx" && strings.HasPrefix(body, "Created ")) {
			return compactModified
		}
	case "edit_line", "insert_lines", "delete_lines":
		// Legacy built-ins used ordinary success text and error-prefixed failures.
		return compactModified
	}
	return compactReferenced
}

// read_many numbers source lines, so only unnumbered, sequential generated
// headers delimit results. Validate all sections against the terminal counts;
// omitted/unknown sections must not turn failed siblings into successful reads.
func compactBatchReads(body string) []string {
	status := readManyStatusForPrune(body, "")
	var okCount, failedCount int
	if _, err := fmt.Sscanf(status, ", ok=%d, failed=%d", &okCount, &failedCount); err != nil {
		return nil
	}
	var paths []string
	sections, good, bad := 0, 0, 0
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "== [") {
			continue
		}
		sections++
		pathRange, ok := strings.CutPrefix(line, fmt.Sprintf("== [%d] ", sections))
		if !ok || !strings.HasSuffix(pathRange, " ==") || i+1 >= len(lines) {
			return nil
		}
		pathRange = strings.TrimSuffix(pathRange, " ==")
		colon := strings.LastIndexByte(pathRange, ':')
		if colon < 1 {
			return nil
		}
		from, to, found := strings.Cut(pathRange[colon+1:], "-")
		a, e1 := strconv.Atoi(from)
		b, e2 := strconv.Atoi(to)
		if !found || e1 != nil || e2 != nil || a < 1 || b < a {
			return nil
		}
		first := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(first, "error:") {
			bad++
			continue
		}
		number, _, numbered := strings.Cut(first, " |")
		n, err := strconv.Atoi(number)
		// Empty successful ranges have an empty body. Numbered lines are the
		// normal case; any unexpected/truncated body keeps the batch unknown.
		if first != "" && (!numbered || err != nil || n < 1) {
			return nil
		}
		good++
		path := pathRange[:colon]
		if !strings.Contains(path, "...") { // preview may elide a long filename
			paths = append(paths, path)
		}
	}
	if sections != okCount+failedCount || good != okCount || bad != failedCount {
		return nil
	}
	return paths
}
