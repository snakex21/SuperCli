package webgui

import (
	"context"
	"time"

	"supercli/internal/storage/session"
)

type statsLastTurnEvent struct {
	Model        string `json:"model"`
	Input        int64  `json:"tok_in"`
	CachedInput  int64  `json:"tok_cached"`
	Output       int64  `json:"tok_out"`
	Total        int64  `json:"tok_total"`
	Reasoning    int64  `json:"reasoning_tok"`
	HasCached    bool   `json:"has_cached"`
	HasReasoning bool   `json:"has_reasoning"`
}

type statsLastTurnView struct {
	Event     statsLastTurnEvent `json:"ev"`
	Tools     int                `json:"tools"`
	Elapsed   int64              `json:"elapsed"`
	Kind      string             `json:"kind"`
	CreatedAt string             `json:"created_at"`
}

func readStatsLastTurn(ctx context.Context, store *session.Store, sessionID string) (*statsLastTurnView, error) {
	turn, found, err := store.LatestTurnTelemetry(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var main session.UsageRecord
	var mainFound bool
	if !found {
		main, mainFound, err = store.LatestMainUsage(ctx, sessionID, time.Time{})
	} else if turn.DurationMS > 0 {
		// Duration is a saved end-to-end span, not generation time. With no
		// span, a prior main call cannot prove this response's model identity.
		main, mainFound, err = store.LatestMainUsage(ctx, sessionID, turn.CreatedAt,
			turn.CreatedAt.Add(-time.Duration(turn.DurationMS)*time.Millisecond))
	}
	if err != nil {
		return nil, err
	}
	if found {
		// Old summaries do not store the model. Only a main call within the
		// saved time span supplies that identity; the current selector or
		// a later helper call must never relabel a historical response.
		return &statsLastTurnView{
			Event: statsLastTurnEvent{Model: main.Model, Input: turn.Input, CachedInput: turn.CachedInput,
				Output: turn.Output, Total: turn.Input + turn.Output, Reasoning: turn.Reasoning,
				HasCached: turn.HasCachedInput, HasReasoning: turn.HasReasoning},
			Tools: turn.ToolCalls, Elapsed: turn.DurationMS, Kind: "response", CreatedAt: turn.CreatedAt.Format(time.RFC3339Nano),
		}, nil
	}
	if !mainFound {
		return nil, nil
	}
	return &statsLastTurnView{
		Event: statsLastTurnEvent{Model: main.Model, Input: main.Input, CachedInput: main.CachedInput,
			Output: main.Output, Total: main.Input + main.Output, Reasoning: main.Reasoning,
			HasCached: main.HasCachedInput, HasReasoning: main.HasReasoning},
		Kind: "model_call", CreatedAt: main.CreatedAt.Format(time.RFC3339Nano),
	}, nil
}
