package webgui

import "supercli/internal/ui/attachments"

const (
	maxChatAttachments      = attachments.MaxFiles
	maxChatAttachmentBytes  = attachments.MaxFileBytes
	maxChatAttachmentsBytes = attachments.MaxTotalBytes
)

func buildAttachmentAddon(home string, paths []string) (string, error) {
	return attachments.BuildAddon(home, paths)
}
func stagePickedAttachments(home string, paths []string) ([]string, error) {
	return attachments.StagePicked(home, paths)
}
func safeAttachmentName(name string) string { return attachments.SafeName(name) }
