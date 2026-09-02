//go:build windows

package mail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// OlDefaultFolders constants used by the mail tool.
const (
	olFolderDeletedItems = 3
	olFolderSentMail     = 5
	olFolderInbox        = 6
	olFolderDrafts       = 16
	olFolderSyncIssues   = 20
	olFolderJunk         = 23
)

// olMailItem is the OlItemType constant for a mail item.
const olMailItem = 0

// outlookRun executes one outlook_mail operation. COM requires the
// calling goroutine to stay on one OS thread between CoInitialize
// and CoUninitialize, so the whole call is wrapped in
// LockOSThread and runs synchronously (operations are fast and
// local). ctx is honored only coarsely (checked between items).
func outlookRun(ctx context.Context, a outlookArgs) (out string, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if e := ole.CoInitialize(0); e != nil {
		// S_FALSE (already initialized) arrives as an error in
		// go-ole; only fail on real errors.
		if oleErr, ok := e.(*ole.OleError); !ok || oleErr.Code() != 1 { // 1 = S_FALSE
			return "", fmt.Errorf("COM init: %w", e)
		}
	}
	defer ole.CoUninitialize()

	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("Outlook COM panic: %v (is desktop Outlook installed?)", r)
		}
	}()

	app, e := outlookApp()
	if e != nil {
		return "", e
	}
	defer app.Release()

	nsRaw, e := oleutil.CallMethod(app, "GetNamespace", "MAPI")
	if e != nil {
		return "", fmt.Errorf("GetNamespace(MAPI): %w", e)
	}
	ns := nsRaw.ToIDispatch()
	defer ns.Release()

	switch a.Op {
	case "folders":
		return outlookFolders(ns)
	case "list":
		return outlookList(ctx, ns, a)
	case "read":
		return outlookRead(ctx, ns, a)
	case "search":
		return outlookSearch(ctx, ns, a)
	case "count":
		return outlookCount(ctx, ns, a)
	case "sync_status":
		return outlookSyncStatus(ns)
	case "export_msg":
		return outlookExportMSG(ns, a)
	case "draft":
		return outlookDraft(app, a)
	case "trash":
		return outlookTrash(ctx, ns, a)
	case "purge":
		return outlookPurge(ctx, ns, a)
	default:
		return "", fmt.Errorf("unknown op %q (want folders|list|read|search|count|sync_status|export_msg|draft|trash|purge)", a.Op)
	}
}

// outlookApp connects to a running Outlook or starts one.
func outlookApp() (*ole.IDispatch, error) {
	// Prefer the running instance.
	if unk, err := oleutil.GetActiveObject("Outlook.Application"); err == nil && unk != nil {
		if disp, err := unk.QueryInterface(ole.IID_IDispatch); err == nil {
			unk.Release()
			return disp, nil
		}
		unk.Release()
	}
	unk, err := oleutil.CreateObject("Outlook.Application")
	if err != nil {
		return nil, fmt.Errorf("cannot start Outlook (Outlook.Application): %w", err)
	}
	disp, err := unk.QueryInterface(ole.IID_IDispatch)
	unk.Release()
	if err != nil {
		return nil, fmt.Errorf("Outlook IDispatch: %w", err)
	}
	return disp, nil
}

// outlookExportMSG saves one exact Outlook item as a native Unicode .msg file.
// It never deletes or moves the source message and refuses to overwrite an
// existing file, so callers can verify the backup before any destructive step.
func outlookExportMSG(ns *ole.IDispatch, a outlookArgs) (string, error) {
	entryID := strings.TrimSpace(a.EntryID)
	if entryID == "" {
		return "", fmt.Errorf("export_msg: entry_id is required")
	}
	path := strings.TrimSpace(a.Path)
	if path == "" {
		return "", fmt.Errorf("export_msg: path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("export_msg: resolve path: %w", err)
	}
	if !strings.EqualFold(filepath.Ext(abs), ".msg") {
		return "", fmt.Errorf("export_msg: target path must end with .msg")
	}
	if _, err := os.Stat(abs); err == nil {
		return "", fmt.Errorf("export_msg: target already exists: %s", abs)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("export_msg: stat target: %w", err)
	}
	parent := filepath.Dir(abs)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return "", fmt.Errorf("export_msg: target directory does not exist (%s): %w", parent, err)
	}

	v, err := oleutil.CallMethod(ns, "GetItemFromID", entryID)
	if err != nil {
		return "", fmt.Errorf("export_msg: GetItemFromID: %w", err)
	}
	item := v.ToIDispatch()
	defer item.Release()
	subject := prop(item, "Subject")
	attachments := 0
	if av, err := oleutil.GetProperty(item, "Attachments"); err == nil {
		atts := av.ToIDispatch()
		if cv, err := oleutil.GetProperty(atts, "Count"); err == nil {
			attachments = int(toInt64(cv.Value()))
			cv.Clear()
		}
		atts.Release()
	}

	// OlSaveAsType.olMSGUnicode = 9. This preserves Unicode metadata and the
	// complete Outlook item, including attachments, in the original MSG format.
	if _, err := oleutil.CallMethod(item, "SaveAs", abs, 9); err != nil {
		return "", fmt.Errorf("export_msg: Outlook SaveAs(.msg): %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("export_msg: saved file verification: %w", err)
	}
	if info.Size() <= 0 {
		return "", fmt.Errorf("export_msg: Outlook created an empty file: %s", abs)
	}
	return fmt.Sprintf("Exported Outlook message as MSG.\nsubject: %s\npath: %s\nsize: %d bytes\nattachments: %d\nsource_unchanged: true", subject, abs, info.Size(), attachments), nil
}

// prop reads a property; empty string on failure.
func prop(d *ole.IDispatch, name string) string {
	v, err := oleutil.GetProperty(d, name)
	if err != nil {
		return ""
	}
	defer v.Clear()
	return strings.TrimSpace(fmt.Sprintf("%v", v.Value()))
}

// propTime reads a date property as time.Time (zero on failure).
func propTime(d *ole.IDispatch, name string) time.Time {
	v, err := oleutil.GetProperty(d, name)
	if err != nil {
		return time.Time{}
	}
	defer v.Clear()
	if t, ok := v.Value().(time.Time); ok {
		return t
	}
	return time.Time{}
}

// outlookFolders lists every account's folders recursively. Gmail exposes
// important locations such as Trash/Spam/All Mail below a [Gmail] parent, so
// only listing top-level folders makes the agent incorrectly conclude that a
// message does not exist when it merely has no Inbox label.
func outlookFolders(ns *ole.IDispatch) (string, error) {
	foldersRaw, err := oleutil.GetProperty(ns, "Folders")
	if err != nil {
		return "", fmt.Errorf("Folders: %w", err)
	}
	stores := foldersRaw.ToIDispatch()
	defer stores.Release()

	countV, err := oleutil.GetProperty(stores, "Count")
	if err != nil {
		return "", err
	}
	n := int(toInt64(countV.Value()))
	countV.Clear()

	var sb strings.Builder
	sb.WriteString("Outlook folders (recursive; account/folder · item count):\n")
	for i := 1; i <= n; i++ {
		storeV, err := oleutil.GetProperty(stores, "Item", i)
		if err != nil {
			continue
		}
		store := storeV.ToIDispatch()
		storeName := prop(store, "Name")
		_ = walkOutlookFolderChildren(store, storeName, 0, func(path string, folder *ole.IDispatch) error {
			items := ""
			if iv, err := oleutil.GetProperty(folder, "Items"); err == nil {
				it := iv.ToIDispatch()
				if cv, err := oleutil.GetProperty(it, "Count"); err == nil {
					items = fmt.Sprintf(" · %d item(s)", int(toInt64(cv.Value())))
					cv.Clear()
				}
				it.Release()
			}
			fmt.Fprintf(&sb, "  %s%s\n", path, items)
			return nil
		})
		store.Release()
	}
	sb.WriteString("\nUse folder:\"<account>/<folder>/<subfolder>\" for one folder, or scope:\"account\" with search/count to inspect the whole default account.")
	return sb.String(), nil
}

// walkOutlookFolderChildren walks all descendants of parent. The callback may
// inspect folder during the call but must not retain or Release it.
func walkOutlookFolderChildren(parent *ole.IDispatch, basePath string, depth int, fn func(path string, folder *ole.IDispatch) error) error {
	if depth > 12 {
		return nil
	}
	subV, err := oleutil.GetProperty(parent, "Folders")
	if err != nil {
		return nil
	}
	sub := subV.ToIDispatch()
	defer sub.Release()
	countV, err := oleutil.GetProperty(sub, "Count")
	if err != nil {
		return nil
	}
	n := int(toInt64(countV.Value()))
	countV.Clear()
	for i := 1; i <= n; i++ {
		fv, err := oleutil.GetProperty(sub, "Item", i)
		if err != nil {
			continue
		}
		folder := fv.ToIDispatch()
		name := prop(folder, "Name")
		path := name
		if basePath != "" {
			path = basePath + "/" + name
		}
		if err := fn(path, folder); err != nil {
			folder.Release()
			return err
		}
		if err := walkOutlookFolderChildren(folder, path, depth+1, fn); err != nil {
			folder.Release()
			return err
		}
		folder.Release()
	}
	return nil
}

// defaultOutlookStoreRoot returns the root MAPIFolder that contains the
// default Inbox. scope:"account" deliberately stays inside this store rather
// than silently searching unrelated Outlook accounts.
func defaultOutlookStoreRoot(ns *ole.IDispatch) (*ole.IDispatch, error) {
	inboxV, err := oleutil.CallMethod(ns, "GetDefaultFolder", olFolderInbox)
	if err != nil {
		return nil, fmt.Errorf("GetDefaultFolder(Inbox): %w", err)
	}
	inbox := inboxV.ToIDispatch()
	parentV, err := oleutil.GetProperty(inbox, "Parent")
	inbox.Release()
	if err != nil {
		return nil, fmt.Errorf("Inbox.Parent: %w", err)
	}
	return parentV.ToIDispatch(), nil
}

// resolveFolder finds a folder by name/path. Common localized/default-folder
// aliases are resolved through Outlook's GetDefaultFolder first, so callers do
// not need to know that Gmail's Kosz/Spam/Wysłane live below [Gmail]. Paths use
// "/": "account@x.com/[Gmail]/Kosz".
func resolveFolder(ns *ole.IDispatch, spec string) (*ole.IDispatch, error) {
	spec = strings.TrimSpace(spec)
	if id, ok := outlookDefaultFolderAlias(spec); ok {
		v, err := oleutil.CallMethod(ns, "GetDefaultFolder", id)
		if err != nil {
			return nil, fmt.Errorf("GetDefaultFolder(%q): %w", spec, err)
		}
		return v.ToIDispatch(), nil
	}
	parts := strings.Split(spec, "/")
	curRaw, err := oleutil.GetProperty(ns, "Folders")
	if err != nil {
		return nil, err
	}
	cur := curRaw.ToIDispatch()
	var folder *ole.IDispatch
	for depth, part := range parts {
		fv, err := oleutil.GetProperty(cur, "Item", part)
		if err != nil {
			// Single-segment spec: also try it under the default store
			// (e.g. "Sent Items").
			if depth == 0 && len(parts) == 1 {
				cur.Release()
				inbox, ierr := resolveFolder(ns, "")
				if ierr != nil {
					return nil, ierr
				}
				parentV, perr := oleutil.GetProperty(inbox, "Parent")
				inbox.Release()
				if perr != nil {
					return nil, fmt.Errorf("folder %q not found", spec)
				}
				parent := parentV.ToIDispatch()
				subV, serr := oleutil.GetProperty(parent, "Folders")
				parent.Release()
				if serr != nil {
					return nil, fmt.Errorf("folder %q not found", spec)
				}
				sub := subV.ToIDispatch()
				fv2, ferr := oleutil.GetProperty(sub, "Item", part)
				sub.Release()
				if ferr != nil {
					return nil, fmt.Errorf("folder %q not found (use op:folders to list)", spec)
				}
				return fv2.ToIDispatch(), nil
			}
			cur.Release()
			return nil, fmt.Errorf("folder %q not found at segment %q (use op:folders to list)", spec, part)
		}
		folder = fv.ToIDispatch()
		if depth < len(parts)-1 {
			nextV, err := oleutil.GetProperty(folder, "Folders")
			cur.Release()
			folder.Release()
			if err != nil {
				return nil, err
			}
			cur = nextV.ToIDispatch()
		}
	}
	cur.Release()
	return folder, nil
}

func outlookDefaultFolderAlias(spec string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "", "inbox", "skrzynka odbiorcza":
		return olFolderInbox, true
	case "trash", "deleted items", "kosz", "usunięte", "elementy usunięte":
		return olFolderDeletedItems, true
	case "sent", "sent items", "wysłane", "elementy wysłane":
		return olFolderSentMail, true
	case "drafts", "wersje robocze":
		return olFolderDrafts, true
	case "spam", "junk", "junk email", "wiadomości-śmieci", "wiadomości śmieci":
		return olFolderJunk, true
	case "sync issues", "błędy synchronizacji", "problemy z synchronizacją":
		return olFolderSyncIssues, true
	default:
		return 0, false
	}
}

func outlookSyncStatus(ns *ole.IDispatch) (string, error) {
	folderV, err := oleutil.CallMethod(ns, "GetDefaultFolder", olFolderSyncIssues)
	if err != nil {
		return "", fmt.Errorf("sync_status: GetDefaultFolder(Sync Issues): %w", err)
	}
	folder := folderV.ToIDispatch()
	defer folder.Release()
	itemsV, err := oleutil.GetProperty(folder, "Items")
	if err != nil {
		return "", fmt.Errorf("sync_status: Items: %w", err)
	}
	items := itemsV.ToIDispatch()
	defer items.Release()
	_, _ = oleutil.CallMethod(items, "Sort", "[ReceivedTime]", true)
	countV, err := oleutil.GetProperty(items, "Count")
	if err != nil {
		return "", fmt.Errorf("sync_status: Count: %w", err)
	}
	n := int(toInt64(countV.Value()))
	countV.Clear()
	if n == 0 {
		return "No Outlook synchronization issue entries are present.", nil
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	var sb strings.Builder
	shown := 0
	for i := 1; i <= n && shown < 5; i++ {
		iv, err := oleutil.GetProperty(items, "Item", i)
		if err != nil {
			continue
		}
		item := iv.ToIDispatch()
		recv := propTime(item, "ReceivedTime")
		if recv.IsZero() || recv.Before(cutoff) {
			item.Release()
			continue
		}
		body := strings.TrimSpace(prop(item, "Body"))
		body = strings.Join(strings.Fields(body), " ")
		if len(body) > 700 {
			body = body[:700] + "…"
		}
		shown++
		fmt.Fprintf(&sb, "%d. %s — %s\n", shown, recv.Format("2006-01-02 15:04:05"), body)
		item.Release()
	}
	if shown == 0 {
		return fmt.Sprintf("Outlook has %d synchronization issue log entrie(s), but none are newer than 24 hours.", n), nil
	}
	return fmt.Sprintf("WARNING: Outlook has %d recent synchronization issue entrie(s) in the last 24 hours. Local Outlook cache may differ from the remote mail server. Do not treat a local zero-result search as proof that the remote mailbox is empty.\n%s", shown, sb.String()), nil
}

func outlookSyncWarning(ns *ole.IDispatch) string {
	status, err := outlookSyncStatus(ns)
	if err != nil || !strings.HasPrefix(status, "WARNING:") {
		return ""
	}
	first := status
	if idx := strings.IndexByte(first, '\n'); idx >= 0 {
		first = first[:idx]
	}
	return " " + first
}

// sortedItems returns the folder's Items sorted newest-first.
func sortedItems(folder *ole.IDispatch) (*ole.IDispatch, int, error) {
	itemsV, err := oleutil.GetProperty(folder, "Items")
	if err != nil {
		return nil, 0, err
	}
	items := itemsV.ToIDispatch()
	if _, err := oleutil.CallMethod(items, "Sort", "[ReceivedTime]", true); err != nil {
		// Non-mail folders may not sort; continue unsorted.
		_ = err
	}
	cv, err := oleutil.GetProperty(items, "Count")
	if err != nil {
		items.Release()
		return nil, 0, err
	}
	n := int(toInt64(cv.Value()))
	cv.Clear()
	return items, n, nil
}

func mailSummary(item *ole.IDispatch, idx int, withPreview bool) string {
	subject := prop(item, "Subject")
	sender := prop(item, "SenderName")
	addr := prop(item, "SenderEmailAddress")
	recv := propTime(item, "ReceivedTime")
	when := ""
	if !recv.IsZero() {
		when = recv.Format("2006-01-02 15:04")
	}
	id := prop(item, "EntryID")
	line := fmt.Sprintf("%d. [%s] %s — %s <%s>\n   entry_id: %s", idx, when, subject, sender, addr, id)
	if withPreview {
		body := prop(item, "Body")
		body = strings.Join(strings.Fields(body), " ")
		if len(body) > 160 {
			body = body[:160] + "…"
		}
		if body != "" {
			line += "\n   " + body
		}
	}
	return line
}
