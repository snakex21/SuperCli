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
