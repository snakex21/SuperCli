package tools

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parseUserTool is a minimal TOML reader that covers the
// subset we need: [tool] / [tool.execution] sections, scalar
// keys (string, integer, array-of-strings), and one-level
// nesting. We do NOT support multi-level tables, inline
// tables, dotted keys, or arrays of tables. The point is to
// avoid a TOML dependency; if a user needs more, they can
// rename their file to .json (loader in F4.c+1).
func parseUserTool(path string) (UserToolDef, error) {
	f, err := os.Open(path)
	if err != nil {
		return UserToolDef{}, err
	}
	defer f.Close()
	var (
		def   UserToolDef
		sect  string // "tool" | "tool.execution" | ""
		array = ""   // current array key (in [tool.execution])
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sect = strings.TrimSpace(line[1 : len(line)-1])
			array = ""
			continue
		}
		// Array entry: "- value" or "- "value""
		if strings.HasPrefix(line, "-") {
			val := strings.TrimSpace(line[1:])
			val = unquote(val)
			switch array {
			case "args":
				def.Execution.Args = append(def.Execution.Args, val)
			}
			continue
		}
		// Key/value line: "key = value"
		eq := strings.Index(line, "=")
		if eq < 0 {
			return def, fmt.Errorf("line %q: missing '='", line)
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		// End of array declaration: "key = [" started a
		// multiline array; line with "]" closes it. We
		// only support the single-line "key = [..]" form
		// for simplicity.
		if strings.HasPrefix(v, "[") && !strings.HasSuffix(v, "]") {
			return def, fmt.Errorf("multi-line arrays not supported in %q", path)
		}
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			// Inline array of strings: ["a", "b"]
			inner := strings.TrimSpace(v[1 : len(v)-1])
			var arr []string
			if inner != "" {
				for _, part := range splitArray(inner) {
					arr = append(arr, unquote(strings.TrimSpace(part)))
				}
			}
			if err := setField(&def, sect, k, arr); err != nil {
				return def, err
			}
			array = ""
			continue
		}
		// Detect multiline array start: "key = ["
		if v == "[" {
			array = k
			continue
		}
		// Detect array close on its own line
		if v == "]" {
			array = ""
			continue
		}
		// Strip trailing inline comment.
		if i := indexUnquotedHash(v); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if err := setField(&def, sect, k, unquote(v)); err != nil {
			return def, err
		}
	}
	if err := sc.Err(); err != nil {
		return def, err
	}
	return def, nil
}

// setField dispatches a parsed (key, value) into the right
// UserToolDef field based on the current section. Only the
// subset of fields the F4 design needs is wired.
func setField(d *UserToolDef, section, key string, value any) error {
	switch section {
	case "tool":
		switch key {
		case "name":
			d.Name = toString(value)
		case "description":
			d.Description = toString(value)
		case "version":
			d.Version = toString(value)
		case "schema":
			d.Schema = toString(value)
		default:
			return fmt.Errorf("unknown [tool] key %q", key)
		}
	case "tool.execution", "execution":
		switch key {
		case "type":
			d.Execution.Type = toString(value)
		case "command":
			d.Execution.Command = toString(value)
		case "body":
			d.Execution.Body = toString(value)
		case "timeout_ms":
			n, err := strconv.Atoi(toString(value))
			if err != nil {
				return fmt.Errorf("timeout_ms: %w", err)
			}
			d.Execution.TimeoutMs = n
		case "args":
			if arr, ok := value.([]string); ok {
				d.Execution.Args = arr
			} else {
				return fmt.Errorf("args must be array of strings")
			}
		default:
			return fmt.Errorf("unknown [tool.execution] key %q", key)
		}
	default:
		return fmt.Errorf("unknown section %q", section)
	}
	return nil
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && (s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// splitArray splits a top-level comma-separated list,
// respecting double-quoted segments. The format is good
// enough for ["a", "b", "c"]; anything fancier (nested
// arrays, escapes) is not supported.
func splitArray(s string) []string {
	var out []string
	var cur strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inStr = !inStr
			cur.WriteByte(c)
			continue
		}
		if c == ',' && !inStr {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// indexUnquotedHash returns the index of the first '#' that
// is NOT inside a quoted string, or -1.
func indexUnquotedHash(s string) int {
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inStr = !inStr
			continue
		}
		if c == '#' && !inStr {
			return i
		}
	}
	return -1
}
