package checkpoint

import (
	"context"
	"encoding/json"

	"supercli/internal/tools/ctxexec"
)

func checkpointCommandLifetime(ctx context.Context, name string, args json.RawMessage) (context.Context, context.CancelFunc, error) {
	if name != "ctx_execute" {
		return ctx, func() {}, nil
	}
	var request struct {
		TimeoutMS int `json:"timeout_ms"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, nil, err
	}
	return ctxexec.RequestLifetime(ctx, request.TimeoutMS)
}
