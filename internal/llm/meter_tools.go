package llm

import "sort"

// ToolUsage is the estimated prompt cost of one tool's results.
type ToolUsage struct {
	Name   string // tool name; "" aggregates the tail beyond the report limit
	Tokens int
	Count  int // number of result messages
}

// ToolResultTally attributes tool-result tokens to tool names in a single
// pass over a transcript: assistant tool calls map call IDs to names, and
// tool messages (which may lack a Name) are resolved through that map.
type ToolResultTally struct {
	names map[string]string
	by    map[string]*ToolUsage
}

// Observe feeds one message with its already-estimated token cost.
// Only assistant tool calls and tool results are relevant.
func (c *ToolResultTally) Observe(m Message, tokens int) {
	switch m.Role {
	case RoleAssistant:
		for _, tc := range m.ToolCalls {
			if tc.ID == "" || tc.Name == "" {
				continue
			}
			if c.names == nil {
				c.names = make(map[string]string, len(m.ToolCalls))
			}
			c.names[tc.ID] = tc.Name
		}
	case RoleTool:
		name := m.Name
		if name == "" && m.ToolCallID != "" {
			name = c.names[m.ToolCallID]
		}
		if name == "" {
			name = "?"
		}
		if c.by == nil {
			c.by = make(map[string]*ToolUsage)
		}
		u := c.by[name]
		if u == nil {
			u = &ToolUsage{Name: name}
			c.by[name] = u
		}
		u.Tokens += tokens
		u.Count++
	}
}

// Report returns tools sorted by tokens (desc, then name). With limit > 0
// and more tools than limit, the tail is folded into one entry with an
// empty Name.
func (c *ToolResultTally) Report(limit int) []ToolUsage {
	if len(c.by) == 0 {
		return nil
	}
	all := make([]ToolUsage, 0, len(c.by))
	for _, u := range c.by {
		all = append(all, *u)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Tokens != all[j].Tokens {
			return all[i].Tokens > all[j].Tokens
		}
		return all[i].Name < all[j].Name
	})
	if limit <= 0 || len(all) <= limit {
		return all
	}
	out := append([]ToolUsage(nil), all[:limit]...)
	var rest ToolUsage
	for _, u := range all[limit:] {
		rest.Tokens += u.Tokens
		rest.Count += u.Count
	}
	return append(out, rest)
}
