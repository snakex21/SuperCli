package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

// A bounded UI journal, separate from worker evidence sent to the model. It
// rides the existing transcript write once per handoff, not one SQL write per
// tool. Calls without a matching result remain explicitly incomplete.
type workerUIActivity struct {
	ParentCallID string             `json:"parent_call_id,omitempty"`
	Version      int                `json:"version"`
	ID           string             `json:"id"`
	Run          int                `json:"run"`
	Calls        []workerUIToolCall `json:"calls"`
	Omitted      int                `json:"omitted,omitempty"`
}

type workerUIToolCall struct {
	key          [32]byte
	ID           string `json:"id"`
	Tool         string `json:"tool"`
	Args         string `json:"args,omitempty"`
	Output       string `json:"output,omitempty"`
	Error        string `json:"error,omitempty"`
	Completed    bool   `json:"completed"`
	DetailHandle string `json:"detail_handle,omitempty"`
	Partial      bool   `json:"partial,omitempty"`
}

const workerUICallLimit = 256
const workerUIByteLimit = 512 * 1024

func (a *workerUIActivity) call(e ToolCallEvent, detail ...string) {
	if len(a.Calls) == workerUICallLimit {
		copy(a.Calls, a.Calls[1:])
		a.Calls = a.Calls[:len(a.Calls)-1]
		a.Omitted++
	}
	args := strings.Clone(strings.ToValidUTF8(core.HeadTail(e.Args, 700, 240), ""))
	a.Calls = append(a.Calls, workerUIToolCall{key: sha256.Sum256([]byte(e.ID)), ID: core.HeadTail(e.ID, 160, 0), Tool: core.HeadTail(e.Name, 160, 0), Args: args})
	c := &a.Calls[len(a.Calls)-1]
	c.Partial = c.Args != e.Args
	if len(detail) > 0 {
		c.DetailHandle = detail[0]
	}
}

func (a *workerUIActivity) result(e ToolResultEvent, detail ...string) {
	key := sha256.Sum256([]byte(e.ID))
	for i := len(a.Calls) - 1; i >= 0; i-- {
		c := &a.Calls[i]
		if c.key != key || c.Completed {
			continue
		}
		c.Completed = true
		c.Output = core.HeadTail(e.Output, 220, 80)
		if e.Err != nil {
			c.Error = core.HeadTail(e.Err.Error(), 180, 60)
		}
		c.Partial = c.Partial || c.Output != e.Output || e.Err != nil && c.Error != e.Err.Error()
		if len(detail) > 0 {
			c.DetailHandle = detail[0]
		}
		return
	}
}

func (a *workerUIActivity) encode() json.RawMessage {
	for {
		data, _ := json.Marshal(a)
		if len(data) <= workerUIByteLimit {
			return data
		}
		if len(a.Calls) == 0 {
			return nil
		}
		// Keep every call visible: shed the oldest previews before whole calls.
		trimmed := false
		for i := range a.Calls {
			c := &a.Calls[i]
			if c.Output != "" || len(c.Args) > 160 {
				c.Output, c.Args, c.Partial = "", core.HeadTail(c.Args, 120, 40), true
				trimmed = true
				break
			}
		}
		if !trimmed {
			a.Calls = a.Calls[1:]
			a.Omitted++
		}
	}
}

type workerUIActivityKey struct{}

func workerActivityOrigin(data json.RawMessage) *llm.ContextOrigin {
	if len(data) == 0 {
		return nil
	}
	return &llm.ContextOrigin{WorkerActivity: data}
}

func withoutWorkerUI(msg llm.Message) llm.Message {
	return msg.WithoutWorkerActivity()
}

func workerUIContext(ctx context.Context, data json.RawMessage) context.Context {
	return context.WithValue(ctx, workerUIActivityKey{}, data)
}
