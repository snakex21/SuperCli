package metrics

import "sync/atomic"

// Throttled counts requests rejected for rate limiting (exported to /metrics).
var Throttled atomic.Int64
