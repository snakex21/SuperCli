package mail

import (
	"context"
	"time"
)

// OutlookIndexMessage is a read-only snapshot used by the document index.
// The attachment list contains names only; attachments are never saved or
// opened as a side effect of indexing the mailbox.
type OutlookIndexMessage struct {
	EntryID         string
	Folder          string
	Subject         string
	Sender          string
	SenderAddress   string
	To              string
	CC              string
	ReceivedAt      time.Time
	ModifiedAt      time.Time
	Body            string
	AttachmentNames []string
}

// IndexOutlookMessages reads the newest mail from one local Outlook folder.
// It never sends, moves, deletes or marks messages as read.
func IndexOutlookMessages(ctx context.Context, folder string, maxMessages int) ([]OutlookIndexMessage, error) {
	if maxMessages <= 0 {
		maxMessages = 250
	}
	if maxMessages > 2000 {
		maxMessages = 2000
	}
	return outlookIndexMessages(ctx, folder, maxMessages)
}
