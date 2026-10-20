package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"supercli/internal/tools/core"
)

// WorkerToolDetail is display-only data in the existing portable output cache.
// Neither the full payload nor its handle is added to the model's messages.
type WorkerToolDetail struct {
	Args   string `json:"args,omitempty"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

const WorkerToolDetailLimit = 16 * 1024 * 1024

func retainWorkerToolDetail(ctx context.Context, store core.OutputPersistence, call ToolCallEvent, result ToolResultEvent) (handle string, partial bool) {
	detail := WorkerToolDetail{Args: call.Args, Output: result.Output}
	if result.Err != nil {
		detail.Error = result.Err.Error()
	}
	partial = core.HeadTail(detail.Args, 700, 240) != detail.Args ||
		core.HeadTail(detail.Output, 220, 80) != detail.Output ||
		core.HeadTail(detail.Error, 180, 60) != detail.Error
	if !partial || store == nil || len(detail.Args)+len(detail.Output)+len(detail.Error) > WorkerToolDetailLimit {
		return "", partial
	}
	data, err := json.Marshal(detail)
	if err != nil || len(data) > WorkerToolDetailLimit {
		return "", partial
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return "", partial
	}
	handle = "worker_ui_" + hex.EncodeToString(id[:])
	saveCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = store.SaveToolOutput(saveCtx, handle, string(data)); err != nil {
		return "", partial
	}
	return handle, partial
}
