//go:build windows

package mail

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func outlookIndexMessages(ctx context.Context, folderName string, maxMessages int) (messages []OutlookIndexMessage, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if initErr := ole.CoInitialize(0); initErr != nil {
		if oleErr, ok := initErr.(*ole.OleError); !ok || oleErr.Code() != 1 {
			return nil, fmt.Errorf("COM init: %w", initErr)
		}
	}
	defer ole.CoUninitialize()
	defer func() {
		if recovered := recover(); recovered != nil {
			messages, err = nil, fmt.Errorf("Outlook COM panic: %v", recovered)
		}
	}()

	app, err := outlookApp()
	if err != nil {
		return nil, err
	}
	defer app.Release()
	namespaceValue, err := oleutil.CallMethod(app, "GetNamespace", "MAPI")
	if err != nil {
		return nil, fmt.Errorf("GetNamespace(MAPI): %w", err)
	}
	namespace := namespaceValue.ToIDispatch()
	defer namespace.Release()
	folder, err := resolveFolder(namespace, folderName)
	if err != nil {
		return nil, err
	}
	defer folder.Release()
	items, count, err := sortedItems(folder)
	if err != nil {
		return nil, err
	}
	defer items.Release()
	resolvedFolder := prop(folder, "Name")

	messages = make([]OutlookIndexMessage, 0, min(maxMessages, count))
	for index := 1; index <= count && len(messages) < maxMessages; index++ {
		if err := ctx.Err(); err != nil {
			return messages, err
		}
		value, itemErr := oleutil.GetProperty(items, "Item", index)
		if itemErr != nil {
			continue
		}
		item := value.ToIDispatch()
		messageClass := prop(item, "MessageClass")
		if messageClass != "" && !strings.HasPrefix(messageClass, "IPM.Note") {
			item.Release()
			continue
		}
		body := strings.TrimSpace(prop(item, "Body"))
		if len(body) > 50_000 {
			body = body[:50_000]
		}
		message := OutlookIndexMessage{
			EntryID: prop(item, "EntryID"), Folder: resolvedFolder,
			Subject: prop(item, "Subject"), Sender: prop(item, "SenderName"),
			SenderAddress: prop(item, "SenderEmailAddress"), To: prop(item, "To"), CC: prop(item, "CC"),
			ReceivedAt: propTime(item, "ReceivedTime"), ModifiedAt: propTime(item, "LastModificationTime"), Body: body,
			AttachmentNames: outlookAttachmentNames(item),
		}
		item.Release()
		if message.EntryID != "" {
			messages = append(messages, message)
		}
	}
	return messages, nil
}

func outlookAttachmentNames(item *ole.IDispatch) []string {
	value, err := oleutil.GetProperty(item, "Attachments")
	if err != nil {
		return nil
	}
	attachments := value.ToIDispatch()
	defer attachments.Release()
	countValue, err := oleutil.GetProperty(attachments, "Count")
	if err != nil {
		return nil
	}
	count := int(toInt64(countValue.Value()))
	countValue.Clear()
	names := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		attachmentValue, itemErr := oleutil.GetProperty(attachments, "Item", index)
		if itemErr != nil {
			continue
		}
		attachment := attachmentValue.ToIDispatch()
		name := prop(attachment, "FileName")
		attachment.Release()
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}
