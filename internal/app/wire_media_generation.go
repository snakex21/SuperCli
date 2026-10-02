package app

import (
	"context"
	"log"

	"supercli/internal/system/config"
	"supercli/internal/tools"
	"supercli/internal/tools/interactive"
	"supercli/internal/tools/mediagen"
)

func registerMediaGeneration(reg *tools.Registry, home, dataDir string, ask chan<- tools.AskRequest) {
	cfg, err := config.LoadMediaGeneration(dataDir)
	if err != nil {
		log.Printf("media generation configuration: %v", err)
		return
	}
	confirm := func(ctx context.Context, question string) error { return interactive.ConfirmAction(ctx, ask, question) }
	reg.MustRegister(mediagen.NewImage(home, cfg.Image, confirm).Spec())
	reg.MustRegister(mediagen.NewVideo(home, cfg.Video, confirm).Spec())
}
