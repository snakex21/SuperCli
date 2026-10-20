package testbrowser

import "context"

// MemoryMetrics is a development-only CDP snapshot, not process RSS or a peak.
// DOM counters include disconnected nodes awaiting collection and other documents
// in this renderer; callers should also count their connected document nodes.
type MemoryMetrics struct {
	UsedSize         float64 `json:"usedSize"`
	TotalSize        float64 `json:"totalSize"`
	Documents        int     `json:"documents"`
	Nodes            int     `json:"nodes"`
	JSEventListeners int     `json:"jsEventListeners"`
}

// CollectGarbage requests full collection only in the owned test target.
func (p *Page) CollectGarbage(ctx context.Context) error {
	return p.browser.cdp.call(ctx, p.session, "HeapProfiler.collectGarbage", nil, nil)
}

// MemoryMetrics queries explicit heap and DOM counters. Unsupported commands
// return an error rather than silently reporting zero or inventing RSS values.
func (p *Page) MemoryMetrics(ctx context.Context) (MemoryMetrics, error) {
	var m MemoryMetrics
	if err := p.browser.cdp.call(ctx, p.session, "Runtime.getHeapUsage", nil, &m); err != nil {
		return m, err
	}
	var dom MemoryMetrics
	if err := p.browser.cdp.call(ctx, p.session, "Memory.getDOMCounters", nil, &dom); err != nil {
		return m, err
	}
	m.Documents, m.Nodes, m.JSEventListeners = dom.Documents, dom.Nodes, dom.JSEventListeners
	return m, nil
}
