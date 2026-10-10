package webgui

import (
	"context"

	"supercli/internal/system/config"
)

// Refresh before building the loop and its usage identity. A worker override
// refreshes its own endpoint without inheriting the coordinator's window, and
// repeated delegations add no metadata requests.
func (e *Engine) refreshLocalContextWindows(ctx context.Context) error {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	configs := []config.Config{cfg}
	if worker := e.taskWorkerConfig(e.tomlConfig()); worker != nil {
		configs = append(configs, *worker)
	}
	return config.RefreshLocalContextWindows(ctx, configs...)
}
