package agent

import (
	"strconv"
	"sync/atomic"
)

// Tool calls parsed out of PROSE (the thin sentinel protocol and the
// XML fallback) have no provider-assigned id — we mint one. The id
// must be unique for the whole lifetime of a conversation: every
// major provider rejects a request whose history contains two tool
// calls with the same id ("Duplicate value for 'tool_call_id'"), and
// a long session naturally calls the same tool many times.
//
// A process-wide atomic counter is enough: ids only have to be unique
// within one request, and one process never builds two histories that
// share a counter value.
var syntheticToolCallSeq atomic.Uint64

// syntheticToolCallID returns a unique id for a tool call the model
// wrote as text. prefix names the syntax it came from ("sentinel",
// "xml") so the origin stays visible in logs and traces.
func syntheticToolCallID(prefix, name string) string {
	n := syntheticToolCallSeq.Add(1)
	return prefix + "_" + name + "_" + strconv.FormatUint(n, 10)
}
