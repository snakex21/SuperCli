//go:build windows

package mail

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func outlookList(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	folder, err := resolveFolder(ns, a.Folder)
	if err != nil {
		return "", err
	}
	defer folder.Release()
	items, n, err := sortedItems(folder)
	if err != nil {
		return "", err
	}
	defer items.Release()

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d message(s), newest first:\n\n", prop(folder, "Name"), n)
	shown := 0
	for i := 1; i <= n && shown < a.Count; i++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		iv, err := oleutil.GetProperty(items, "Item", i)
		if err != nil {
			continue
		}
		item := iv.ToIDispatch()
		if prop(item, "MessageClass") != "" && !strings.HasPrefix(prop(item, "MessageClass"), "IPM.Note") {
			item.Release()
			continue // skip meeting requests, reports, etc.
		}
		shown++
		sb.WriteString(mailSummary(item, shown, true))
		sb.WriteString("\n\n")
		item.Release()
	}
	if shown == 0 {
		sb.WriteString("(no mail items)\n")
	}
	sb.WriteString("Use op:read with entry_id (or index) for the full body.")
	return sb.String(), nil
}

func outlookRead(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	var item *ole.IDispatch
	if strings.TrimSpace(a.EntryID) != "" {
		v, err := oleutil.CallMethod(ns, "GetItemFromID", a.EntryID)
		if err != nil {
			return "", fmt.Errorf("GetItemFromID: %w", err)
		}
		item = v.ToIDispatch()
	} else if a.Index > 0 {
		folder, err := resolveFolder(ns, a.Folder)
		if err != nil {
			return "", err
		}
		items, n, err := sortedItems(folder)
		folder.Release()
		if err != nil {
			return "", err
		}
		if a.Index > n {
			items.Release()
			return "", fmt.Errorf("index %d out of range (folder has %d items)", a.Index, n)
		}
		v, err := oleutil.GetProperty(items, "Item", a.Index)
		items.Release()
		if err != nil {
			return "", err
		}
		item = v.ToIDispatch()
	} else {
		return "", fmt.Errorf("read: pass entry_id or index (1 = newest)")
	}
	defer item.Release()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	recv := propTime(item, "ReceivedTime")
	when := ""
	if !recv.IsZero() {
		when = recv.Format("2006-01-02 15:04")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Subject: %s\nFrom: %s <%s>\nTo: %s\nCC: %s\nDate: %s\nentry_id: %s\n\n%s",
		prop(item, "Subject"), prop(item, "SenderName"), prop(item, "SenderEmailAddress"),
		prop(item, "To"), prop(item, "CC"), when, prop(item, "EntryID"),
		strings.TrimSpace(prop(item, "Body")))
	out := sb.String()
	const maxBody = 50000
	if len(out) > maxBody {
		out = out[:maxBody] + "\n\n[body truncated]"
	}
	return out, nil
}

func outlookSearch(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	if a.From == "" && a.Subject == "" && a.Since == "" && a.Until == "" {
		return "", fmt.Errorf("search: pass at least one of from/subject/since/until")
	}
	var since, until time.Time
	var err error
	if a.Since != "" {
		if since, err = time.ParseInLocation("2006-01-02", a.Since, time.Local); err != nil {
			return "", fmt.Errorf("bad since date %q (want YYYY-MM-DD)", a.Since)
		}
	}
	if a.Until != "" {
		if until, err = time.ParseInLocation("2006-01-02", a.Until, time.Local); err != nil {
			return "", fmt.Errorf("bad until date %q (want YYYY-MM-DD)", a.Until)
		}
		until = until.Add(24*time.Hour - time.Second)
	}

	folder, err := resolveFolder(ns, a.Folder)
	if err != nil {
		return "", err
	}
	defer folder.Release()
	items, n, err := sortedItems(folder)
	if err != nil {
		return "", err
	}
	defer items.Release()

	// Linear scan, newest first, capped so a huge mailbox cannot
	// stall the agent. Restrict() syntax is fragile across Outlook
	// versions; in-Go filtering is predictable.
	const scanCap = 1000
	var sb strings.Builder
	matched := 0
	for i := 1; i <= n && i <= scanCap && matched < a.Count; i++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		iv, err := oleutil.GetProperty(items, "Item", i)
		if err != nil {
			continue
		}
		item := iv.ToIDispatch()
		recv := propTime(item, "ReceivedTime")
		ok := true
		if !since.IsZero() && (recv.IsZero() || recv.Before(since)) {
			ok = false
			// Items are sorted newest first: once older than
			// 'since', everything after is older too.
			if !recv.IsZero() {
				item.Release()
				break
			}
		}
		if ok && !until.IsZero() && !recv.IsZero() && recv.After(until) {
			ok = false
		}
		if ok && a.From != "" {
			f := strings.ToLower(a.From)
			if !strings.Contains(strings.ToLower(prop(item, "SenderName")), f) &&
				!strings.Contains(strings.ToLower(prop(item, "SenderEmailAddress")), f) {
				ok = false
			}
		}
		if ok && a.Subject != "" &&
			!strings.Contains(strings.ToLower(prop(item, "Subject")), strings.ToLower(a.Subject)) {
			ok = false
		}
		if ok {
			matched++
			sb.WriteString(mailSummary(item, matched, true))
			sb.WriteString("\n\n")
		}
		item.Release()
	}
	if matched == 0 {
		return "No matching messages.", nil
	}
	return fmt.Sprintf("%d match(es):\n\n%sUse op:read with entry_id for the full body.", matched, sb.String()), nil
}

// outlookDraft creates a DRAFT (MailItem.Save). It never calls
// .Send — sending stays a human decision inside Outlook.
func outlookDraft(app *ole.IDispatch, a outlookArgs) (string, error) {
	if strings.TrimSpace(a.Subject) == "" && strings.TrimSpace(a.Body) == "" {
		return "", fmt.Errorf("draft: subject or body required")
	}
	v, err := oleutil.CallMethod(app, "CreateItem", olMailItem)
	if err != nil {
		return "", fmt.Errorf("CreateItem: %w", err)
	}
	item := v.ToIDispatch()
	defer item.Release()
	if a.To != "" {
		if _, err := oleutil.PutProperty(item, "To", a.To); err != nil {
			return "", fmt.Errorf("set To: %w", err)
		}
	}
	if a.CC != "" {
		if _, err := oleutil.PutProperty(item, "CC", a.CC); err != nil {
			return "", fmt.Errorf("set CC: %w", err)
		}
	}
	if _, err := oleutil.PutProperty(item, "Subject", a.Subject); err != nil {
		return "", fmt.Errorf("set Subject: %w", err)
	}
	if _, err := oleutil.PutProperty(item, "Body", a.Body); err != nil {
		return "", fmt.Errorf("set Body: %w", err)
	}
	if _, err := oleutil.CallMethod(item, "Save"); err != nil {
		return "", fmt.Errorf("Save draft: %w", err)
	}
	return fmt.Sprintf("Draft saved to the Outlook Drafts folder (to: %s, subject: %q). It was NOT sent — review and send it in Outlook.", a.To, a.Subject), nil
}

// toInt64 normalizes the numeric types COM may return for Count.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
