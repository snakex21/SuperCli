package webgui

import (
	"log"

	"supercli/internal/system/config"
	"supercli/internal/tools"
	"supercli/internal/tools/mediagen"
)

func (e *Engine) registerMediaGeneration(reg *tools.Registry, home string) {
	cfg, err := config.LoadMediaGeneration(e.DataDir())
	if err != nil {
		log.Printf("media generation configuration: %v", err)
		return
	}
	keys := func(name string) (string, string, bool) { return config.MediaGenerationProviderKey(e.DataDir(), name) }
	reg.MustRegister(mediagen.NewImage(home, cfg.Image).WithProviderKeys(keys).Spec())
	reg.MustRegister(mediagen.NewVideo(home, cfg.Video).WithProviderKeys(keys).Spec())
	for _, name := range mediagen.AdvertisedTools(cfg) {
		reg.MarkAlwaysOn(name)
	}
}
