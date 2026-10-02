package webgui

import (
	"context"
	"log"

	"supercli/internal/system/config"
	"supercli/internal/tools"
	"supercli/internal/tools/interactive"
	"supercli/internal/tools/mediagen"
)

func (e *Engine) registerMediaGeneration(reg *tools.Registry, home string) {
	cfg, err := config.LoadMediaGeneration(e.DataDir())
	if err != nil {
		log.Printf("media generation configuration: %v", err)
		return
	}
	confirm := func(ctx context.Context, question string) error { return interactive.ConfirmAction(ctx, nil, question) }
	reg.MustRegister(mediagen.NewImage(home, cfg.Image, confirm).Spec())
	reg.MustRegister(mediagen.NewVideo(home, cfg.Video, confirm).Spec())
}
