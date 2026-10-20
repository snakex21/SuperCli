package stats

import "supercli/internal/llm"

func (m *Memory) RecordContextUsage(u llm.ContextUsage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil || u.Empty() {
		return
	}
	if m.cur.Context == nil {
		m.cur.Context = &llm.ContextUsage{}
	}
	m.cur.Context.Add(u)
}

func SumContextUsage(turns []Turn, calls []Call) *llm.ContextUsage {
	var out *llm.ContextUsage
	add := func(u *llm.ContextUsage) {
		if u == nil || u.Empty() {
			return
		}
		if out == nil {
			out = &llm.ContextUsage{}
		}
		out.Add(*u)
	}
	for _, turn := range turns {
		add(turn.Context)
	}
	for _, call := range calls {
		add(call.Context)
	}
	return out
}
