//go:build windows

package mail

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// olFolderInbox is the OlDefaultFolders constant for the Inbox.
const olFolderInbox = 6

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
	case "draft":
		return outlookDraft(app, a)
	default:
		return "", fmt.Errorf("unknown op %q (want folders|list|read|search|draft)", a.Op)
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

// outlookFolders lists every account's top-level mail folders.
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
	sb.WriteString("Outlook folders (account/folder · item count):\n")
	for i := 1; i <= n; i++ {
		storeV, err := oleutil.GetProperty(stores, "Item", i)
		if err != nil {
			continue
		}
		store := storeV.ToIDispatch()
		storeName := prop(store, "Name")
		subRaw, err := oleutil.GetProperty(store, "Folders")
		if err != nil {
			store.Release()
			continue
		}
		sub := subRaw.ToIDispatch()
		scV, err := oleutil.GetProperty(sub, "Count")
		if err == nil {
			sc := int(toInt64(scV.Value()))
			scV.Clear()
			for j := 1; j <= sc; j++ {
				fv, err := oleutil.GetProperty(sub, "Item", j)
				if err != nil {
					continue
				}
				f := fv.ToIDispatch()
				name := prop(f, "Name")
				items := ""
				if iv, err := oleutil.GetProperty(f, "Items"); err == nil {
					it := iv.ToIDispatch()
					if cv, err := oleutil.GetProperty(it, "Count"); err == nil {
						items = fmt.Sprintf(" · %d item(s)", int(toInt64(cv.Value())))
						cv.Clear()
					}
					it.Release()
				}
				fmt.Fprintf(&sb, "  %s/%s%s\n", storeName, name, items)
				f.Release()
			}
		}
		sub.Release()
		store.Release()
	}
	sb.WriteString("\nUse 'list' with folder:\"Inbox\" or folder:\"<account>/<folder>\".")
	return sb.String(), nil
}

// resolveFolder finds a folder by name/path; "" or "inbox" = the
// default Inbox. Paths use "/": "account@x.com/Sent Items".
func resolveFolder(ns *ole.IDispatch, spec string) (*ole.IDispatch, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "inbox") {
		v, err := oleutil.CallMethod(ns, "GetDefaultFolder", olFolderInbox)
		if err != nil {
			return nil, fmt.Errorf("GetDefaultFolder(Inbox): %w", err)
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
