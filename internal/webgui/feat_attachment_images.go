package webgui

import (
	"supercli/internal/llm"
	"supercli/internal/ui/attachments"
)

const (
	directImageMaxDimension = attachments.ImageMaxDimension
	directImageMaxBytes     = attachments.ImageMaxBytes
)

func buildDirectAttachmentImages(home string, paths []string) ([]llm.ImageRef, error) {
	return attachments.BuildImages(home, paths)
}

// Follow the provider's existing vision-attempt contract. An absent capability
// method is unknown, not an authoritative text-only result.
func attachmentInputsForProvider(provider llm.Provider, images []llm.ImageRef, addon string) ([]llm.ImageRef, string) {
	if vision, ok := provider.(interface{ SupportsVision() bool }); ok && !vision.SupportsVision() && len(images) > 0 {
		return nil, addon + "\n[attachment_capability]The selected provider is text-only. No attachment image pixels or video/audio analysis are available in this request. Do not claim to have seen the images or video.[/attachment_capability]"
	}
	return images, addon
}
