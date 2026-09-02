//go:build windows

package mail

import (
	"context"
	"fmt"
	"sort"
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
	if a.Scope == "account" {
		return outlookSearchAccount(ctx, ns, a)
	}
	if a.Scope == "all_stores" {
		return outlookSearchAllStores(ctx, ns, a)
	}
	if a.Scope != "folder" {
		return "", fmt.Errorf("search: unknown scope %q (want folder|account|all_stores)", a.Scope)
	}
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
		return "No matching messages in the local Outlook cache." + outlookSyncWarning(ns), nil
	}
	return fmt.Sprintf("%d match(es):\n\n%sUse op:read with entry_id for the full body.", matched, sb.String()), nil
}

// outlookSearchAccount searches all mail folders below the default account's
// store root. Results are deduplicated by InternetMessageID (falling back to
// EntryID), because Gmail can expose one logical message through several
// label-backed folders.
func outlookSearchAccount(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	filter, err := newOutlookAccountFilter(a, "search")
	if err != nil {
		return "", err
	}
	root, err := defaultOutlookStoreRoot(ns)
	if err != nil {
		return "", err
	}
	defer root.Release()
	rootName := prop(root, "Name")

	seen := make(map[string]struct{})
	locations := make(map[string]int)
	var details strings.Builder
	unique := 0
	shown := 0
	checked := 0
	const globalScanCap = 20000
	truncated := false

	err = walkOutlookFolderChildren(root, rootName, 0, func(path string, folder *ole.IDispatch) error {
		if checked >= globalScanCap {
			truncated = true
			return nil
		}
		itemsV, err := oleutil.GetProperty(folder, "Items")
		if err != nil {
			return nil
		}
		items := itemsV.ToIDispatch()
		defer items.Release()
		countV, err := oleutil.GetProperty(items, "Count")
		if err != nil {
			return nil
		}
		n := int(toInt64(countV.Value()))
		countV.Clear()
		for i := n; i >= 1 && checked < globalScanCap; i-- {
			if err := ctx.Err(); err != nil {
				return err
			}
			iv, err := oleutil.GetProperty(items, "Item", i)
			if err != nil {
				continue
			}
			checked++
			item := iv.ToIDispatch()
			if cls := prop(item, "MessageClass"); cls != "" && !strings.HasPrefix(cls, "IPM.Note") {
				item.Release()
				continue
			}
			if !filter.matches(item) {
				item.Release()
				continue
			}
			key := strings.TrimSpace(prop(item, "InternetMessageID"))
			if key == "" {
				key = strings.TrimSpace(prop(item, "EntryID"))
			}
			if key == "" {
				key = fmt.Sprintf("%s#%d", path, i)
			}
			locations[path]++
			if _, exists := seen[key]; exists {
				item.Release()
				continue
			}
			seen[key] = struct{}{}
			unique++
			if shown < a.Count {
				shown++
				details.WriteString(mailSummary(item, shown, true))
				fmt.Fprintf(&details, "\n   folder: %s\n\n", path)
			}
			item.Release()
		}
		if checked >= globalScanCap {
			truncated = true
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if unique == 0 {
		warning := outlookSyncWarning(ns)
		if truncated {
			return fmt.Sprintf("No matching messages found across the local default Outlook account within %d scanned items; scan limit reached.%s", checked, warning), nil
		}
		return "No matching messages found across the local default Outlook account." + warning, nil
	}
	locationText := formatOutlookLocations(locations, 10)
	limitNote := ""
	if unique > shown {
		limitNote = fmt.Sprintf(" Showing %d of %d unique matches.", shown, unique)
	}
	if truncated {
		limitNote += fmt.Sprintf(" Scan stopped at %d items, so the true total may be higher.", checked)
	}
	return fmt.Sprintf("%d unique match(es) across the default Outlook account.%s Locations: %s\n\n%sUse op:read with entry_id for the full body.", unique, limitNote, locationText, details.String()), nil
}

func outlookSearchAllStores(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	filter, err := newOutlookAccountFilter(a, "search")
	if err != nil {
		return "", err
	}
	storesV, err := oleutil.GetProperty(ns, "Folders")
	if err != nil {
		return "", fmt.Errorf("search all_stores: Folders: %w", err)
	}
	stores := storesV.ToIDispatch()
	defer stores.Release()
	countV, err := oleutil.GetProperty(stores, "Count")
	if err != nil {
		return "", fmt.Errorf("search all_stores: Count: %w", err)
	}
	storeCount := int(toInt64(countV.Value()))
	countV.Clear()

	seen := make(map[string]struct{})
	locations := make(map[string]int)
	var details strings.Builder
	unique := 0
	shown := 0
	checked := 0
	const globalScanCap = 50000
	truncated := false

	scanFolder := func(path string, folder *ole.IDispatch) error {
		if checked >= globalScanCap {
			truncated = true
			return nil
		}
		itemsV, err := oleutil.GetProperty(folder, "Items")
		if err != nil {
			return nil
		}
		items := itemsV.ToIDispatch()
		defer items.Release()
		countV, err := oleutil.GetProperty(items, "Count")
		if err != nil {
			return nil
		}
		n := int(toInt64(countV.Value()))
		countV.Clear()
		for i := n; i >= 1 && checked < globalScanCap; i-- {
			if err := ctx.Err(); err != nil {
				return err
			}
			iv, err := oleutil.GetProperty(items, "Item", i)
			if err != nil {
				continue
			}
			checked++
			item := iv.ToIDispatch()
			if cls := prop(item, "MessageClass"); cls != "" && !strings.HasPrefix(cls, "IPM.Note") {
				item.Release()
				continue
			}
			if !filter.matches(item) {
				item.Release()
				continue
			}
			key := strings.TrimSpace(prop(item, "InternetMessageID"))
			if key == "" {
				key = strings.TrimSpace(prop(item, "EntryID"))
			}
			if key == "" {
				key = fmt.Sprintf("%s#%d", path, i)
			}
			locations[path]++
			if _, exists := seen[key]; exists {
				item.Release()
				continue
			}
			seen[key] = struct{}{}
			unique++
			if shown < a.Count {
				shown++
				details.WriteString(mailSummary(item, shown, true))
				fmt.Fprintf(&details, "\n   folder: %s\n\n", path)
			}
			item.Release()
		}
		if checked >= globalScanCap {
			truncated = true
		}
		return nil
	}

	for i := 1; i <= storeCount && checked < globalScanCap; i++ {
		storeV, err := oleutil.GetProperty(stores, "Item", i)
		if err != nil {
			continue
		}
		store := storeV.ToIDispatch()
		storeName := prop(store, "Name")
		if err := walkOutlookFolderChildren(store, storeName, 0, scanFolder); err != nil {
			store.Release()
			return "", err
		}
		store.Release()
	}

	if unique == 0 {
		if truncated {
			return fmt.Sprintf("No matching messages found across all Outlook stores within %d scanned items; scan limit reached.", checked), nil
		}
		return "No matching messages found across all Outlook stores.", nil
	}
	locationText := formatOutlookLocations(locations, 12)
	limitNote := ""
	if unique > shown {
		limitNote = fmt.Sprintf(" Showing %d of %d unique matches.", shown, unique)
	}
	if truncated {
		limitNote += fmt.Sprintf(" Scan stopped at %d items, so the true total may be higher.", checked)
	}
	return fmt.Sprintf("%d unique match(es) across all Outlook stores.%s Locations: %s\n\n%sUse op:read with entry_id for the full body or op:export_msg to save the exact item as .msg.", unique, limitNote, locationText, details.String()), nil
}

type outlookAccountFilter struct {
	from    string
	subject string
	since   time.Time
	until   time.Time
}

func newOutlookAccountFilter(a outlookArgs, op string) (outlookAccountFilter, error) {
	if strings.TrimSpace(a.From) == "" && strings.TrimSpace(a.Subject) == "" && strings.TrimSpace(a.Since) == "" && strings.TrimSpace(a.Until) == "" {
		return outlookAccountFilter{}, fmt.Errorf("%s: pass at least one of from/subject/since/until", op)
	}
	f := outlookAccountFilter{from: strings.ToLower(strings.TrimSpace(a.From)), subject: strings.ToLower(strings.TrimSpace(a.Subject))}
	var err error
	if a.Since != "" {
		if f.since, err = time.ParseInLocation("2006-01-02", a.Since, time.Local); err != nil {
			return outlookAccountFilter{}, fmt.Errorf("%s: bad since date %q (want YYYY-MM-DD)", op, a.Since)
		}
	}
	if a.Until != "" {
		if f.until, err = time.ParseInLocation("2006-01-02", a.Until, time.Local); err != nil {
			return outlookAccountFilter{}, fmt.Errorf("%s: bad until date %q (want YYYY-MM-DD)", op, a.Until)
		}
		f.until = f.until.Add(24*time.Hour - time.Second)
	}
	return f, nil
}

func (f outlookAccountFilter) matches(item *ole.IDispatch) bool {
	if f.from != "" {
		name := strings.ToLower(prop(item, "SenderName"))
		addr := strings.ToLower(prop(item, "SenderEmailAddress"))
		if !strings.Contains(name, f.from) && !strings.Contains(addr, f.from) {
			return false
		}
	}
	if f.subject != "" && !strings.Contains(strings.ToLower(prop(item, "Subject")), f.subject) {
		return false
	}
	if !f.since.IsZero() || !f.until.IsZero() {
		recv := propTime(item, "ReceivedTime")
		if !f.since.IsZero() && (recv.IsZero() || recv.Before(f.since)) {
			return false
		}
		if !f.until.IsZero() && (recv.IsZero() || recv.After(f.until)) {
			return false
		}
	}
	return true
}

func formatOutlookLocations(locations map[string]int, limit int) string {
	if len(locations) == 0 {
		return "(none)"
	}
	type pair struct {
		path  string
		count int
	}
	pairs := make([]pair, 0, len(locations))
	for path, count := range locations {
		pairs = append(pairs, pair{path: path, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].path < pairs[j].path
		}
		return pairs[i].count > pairs[j].count
	})
	if limit <= 0 || limit > len(pairs) {
		limit = len(pairs)
	}
	parts := make([]string, 0, limit+1)
	for _, p := range pairs[:limit] {
		parts = append(parts, fmt.Sprintf("%s (%d)", p.path, p.count))
	}
	if len(pairs) > limit {
		parts = append(parts, fmt.Sprintf("+%d more folder(s)", len(pairs)-limit))
	}
	return strings.Join(parts, "; ")
}

func outlookCountAccount(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	filter, err := newOutlookAccountFilter(a, "count")
	if err != nil {
		return "", err
	}
	root, err := defaultOutlookStoreRoot(ns)
	if err != nil {
		return "", err
	}
	defer root.Release()
	rootName := prop(root, "Name")

	seen := make(map[string]struct{})
	locations := make(map[string]int)
	checked := 0
	const globalScanCap = 20000
	truncated := false
	err = walkOutlookFolderChildren(root, rootName, 0, func(path string, folder *ole.IDispatch) error {
		if checked >= globalScanCap {
			truncated = true
			return nil
		}
		itemsV, err := oleutil.GetProperty(folder, "Items")
		if err != nil {
			return nil
		}
		items := itemsV.ToIDispatch()
		defer items.Release()
		countV, err := oleutil.GetProperty(items, "Count")
		if err != nil {
			return nil
		}
		n := int(toInt64(countV.Value()))
		countV.Clear()
		for i := n; i >= 1 && checked < globalScanCap; i-- {
			if err := ctx.Err(); err != nil {
				return err
			}
			iv, err := oleutil.GetProperty(items, "Item", i)
			if err != nil {
				continue
			}
			checked++
			item := iv.ToIDispatch()
			if cls := prop(item, "MessageClass"); cls != "" && !strings.HasPrefix(cls, "IPM.Note") {
				item.Release()
				continue
			}
			if !filter.matches(item) {
				item.Release()
				continue
			}
			key := strings.TrimSpace(prop(item, "InternetMessageID"))
			if key == "" {
				key = strings.TrimSpace(prop(item, "EntryID"))
			}
			if key == "" {
				key = fmt.Sprintf("%s#%d", path, i)
			}
			locations[path]++
			seen[key] = struct{}{}
			item.Release()
		}
		if checked >= globalScanCap {
			truncated = true
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	note := ""
	if truncated {
		note = fmt.Sprintf(" Scan stopped at %d items, so the true total may be higher.", checked)
	}
	return fmt.Sprintf("%d unique matching message(s) across the local default Outlook account. Locations: %s.%s%s", len(seen), formatOutlookLocations(locations, 12), note, outlookSyncWarning(ns)), nil
}

// outlookCount returns only the number of matching messages. It intentionally
// avoids rendering subjects, bodies and entry IDs so bulk-cleanup planning does
// not consume model context with hundreds of mail previews.
func outlookCount(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	if a.Scope == "account" {
		return outlookCountAccount(ctx, ns, a)
	}
	if a.Scope != "folder" {
		return "", fmt.Errorf("count: unknown scope %q (want folder|account)", a.Scope)
	}
	if a.From == "" && a.Subject == "" && a.Since == "" && a.Until == "" {
		return "", fmt.Errorf("count: pass at least one of from/subject/since/until")
	}
	var since, until time.Time
	var err error
	if a.Since != "" {
		if since, err = time.ParseInLocation("2006-01-02", a.Since, time.Local); err != nil {
			return "", fmt.Errorf("count: bad since date %q (want YYYY-MM-DD)", a.Since)
		}
	}
	if a.Until != "" {
		if until, err = time.ParseInLocation("2006-01-02", a.Until, time.Local); err != nil {
			return "", fmt.Errorf("count: bad until date %q (want YYYY-MM-DD)", a.Until)
		}
		until = until.Add(24*time.Hour - time.Second)
	}

	folder, err := resolveFolder(ns, a.Folder)
	if err != nil {
		return "", err
	}
	defer folder.Release()
	itemsV, err := oleutil.GetProperty(folder, "Items")
	if err != nil {
		return "", fmt.Errorf("count: Items: %w", err)
	}
	items := itemsV.ToIDispatch()
	defer items.Release()
	countV, err := oleutil.GetProperty(items, "Count")
	if err != nil {
		return "", fmt.Errorf("count: Count: %w", err)
	}
	n := int(toInt64(countV.Value()))
	countV.Clear()

	fromNeedle := strings.ToLower(strings.TrimSpace(a.From))
	subjectNeedle := strings.ToLower(strings.TrimSpace(a.Subject))
	matched := 0
	const scanCap = 10000
	checked := 0
	for i := n; i >= 1 && checked < scanCap; i-- {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		iv, err := oleutil.GetProperty(items, "Item", i)
		if err != nil {
			continue
		}
		checked++
		item := iv.ToIDispatch()
		if cls := prop(item, "MessageClass"); cls != "" && !strings.HasPrefix(cls, "IPM.Note") {
			item.Release()
			continue
		}
		ok := true
		if fromNeedle != "" {
			name := strings.ToLower(prop(item, "SenderName"))
			addr := strings.ToLower(prop(item, "SenderEmailAddress"))
			ok = strings.Contains(name, fromNeedle) || strings.Contains(addr, fromNeedle)
		}
		if ok && subjectNeedle != "" {
			ok = strings.Contains(strings.ToLower(prop(item, "Subject")), subjectNeedle)
		}
		if ok && (!since.IsZero() || !until.IsZero()) {
			recv := propTime(item, "ReceivedTime")
			if !since.IsZero() && (recv.IsZero() || recv.Before(since)) {
				ok = false
			}
			if ok && !until.IsZero() && (recv.IsZero() || recv.After(until)) {
				ok = false
			}
		}
		if ok {
			matched++
		}
		item.Release()
	}
	folderName := prop(folder, "Name")
	if checked >= scanCap && n > scanCap {
		return fmt.Sprintf("%d matching message(s) found in %s within the first %d scanned items; mailbox is larger, so the true count may be higher.%s", matched, folderName, checked, outlookSyncWarning(ns)), nil
	}
	return fmt.Sprintf("%d matching message(s) in local Outlook folder %s.%s", matched, folderName, outlookSyncWarning(ns)), nil
}

// outlookTrash moves explicitly filtered mail to Outlook's configured
// Deleted Items folder. For Gmail/IMAP this resolves to [Gmail]/Trash (or the
// localized equivalent) through GetDefaultFolder, so no folder name is
// hard-coded. Permanent deletion is a separate, explicit purge operation.
func outlookTrash(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	if !a.Confirm {
		return "", fmt.Errorf("trash: confirm:true is required after explicit user approval")
	}
	if strings.TrimSpace(a.From) == "" && strings.TrimSpace(a.Subject) == "" &&
		strings.TrimSpace(a.Since) == "" && strings.TrimSpace(a.Until) == "" {
		return "", fmt.Errorf("trash: pass at least one filter (from/subject/since/until); refusing unfiltered bulk cleanup")
	}

	var since, until time.Time
	var err error
	if a.Since != "" {
		if since, err = time.ParseInLocation("2006-01-02", a.Since, time.Local); err != nil {
			return "", fmt.Errorf("trash: bad since date %q (want YYYY-MM-DD)", a.Since)
		}
	}
	if a.Until != "" {
		if until, err = time.ParseInLocation("2006-01-02", a.Until, time.Local); err != nil {
			return "", fmt.Errorf("trash: bad until date %q (want YYYY-MM-DD)", a.Until)
		}
		until = until.Add(24*time.Hour - time.Second)
	}

	source, err := resolveFolder(ns, a.Folder)
	if err != nil {
		return "", err
	}
	defer source.Release()

	deletedV, err := oleutil.CallMethod(ns, "GetDefaultFolder", olFolderDeletedItems)
	if err != nil {
		return "", fmt.Errorf("trash: GetDefaultFolder(Deleted Items): %w", err)
	}
	deleted := deletedV.ToIDispatch()
	defer deleted.Release()
	if srcID, dstID := prop(source, "EntryID"), prop(deleted, "EntryID"); srcID != "" && srcID == dstID {
		return "", fmt.Errorf("trash: source folder is already Deleted Items/Trash; use purge only after explicit approval for permanent deletion")
	}

	itemsV, err := oleutil.GetProperty(source, "Items")
	if err != nil {
		return "", fmt.Errorf("trash: Items: %w", err)
	}
	items := itemsV.ToIDispatch()
	defer items.Release()
	countV, err := oleutil.GetProperty(items, "Count")
	if err != nil {
		return "", fmt.Errorf("trash: Count: %w", err)
	}
	n := int(toInt64(countV.Value()))
	countV.Clear()
	deletedBefore := outlookFolderItemCount(deleted)

	fromNeedle := strings.ToLower(strings.TrimSpace(a.From))
	subjectNeedle := strings.ToLower(strings.TrimSpace(a.Subject))
	moved := 0
	matched := 0
	failed := 0
	for i := n; i >= 1 && moved < a.Count; i-- {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		iv, err := oleutil.GetProperty(items, "Item", i)
		if err != nil {
			continue
		}
		item := iv.ToIDispatch()
		if cls := prop(item, "MessageClass"); cls != "" && !strings.HasPrefix(cls, "IPM.Note") {
			item.Release()
			continue
		}
		ok := true
		if fromNeedle != "" {
			name := strings.ToLower(prop(item, "SenderName"))
			addr := strings.ToLower(prop(item, "SenderEmailAddress"))
			ok = strings.Contains(name, fromNeedle) || strings.Contains(addr, fromNeedle)
		}
		if ok && subjectNeedle != "" {
			ok = strings.Contains(strings.ToLower(prop(item, "Subject")), subjectNeedle)
		}
		if ok && (!since.IsZero() || !until.IsZero()) {
			recv := propTime(item, "ReceivedTime")
			if !since.IsZero() && (recv.IsZero() || recv.Before(since)) {
				ok = false
			}
			if ok && !until.IsZero() && (recv.IsZero() || recv.After(until)) {
				ok = false
			}
		}
		if !ok {
			item.Release()
			continue
		}
		matched++
		mv, moveErr := oleutil.CallMethod(item, "Move", deleted)
		item.Release()
		if moveErr != nil {
			failed++
			continue
		}
		if mv != nil {
			mv.Clear()
		}
		moved++
	}

	sourceName := prop(source, "Name")
	deletedName := prop(deleted, "Name")
	more := ""
	if moved == a.Count {
		more = " The per-call safety limit was reached; repeat the same confirmed operation if more matching messages remain."
	}
	if failed > 0 {
		more += fmt.Sprintf(" %d matching message(s) could not be moved.", failed)
	}
	sourceAfter := outlookFolderItemCount(source)
	deletedAfter := outlookFolderItemCount(deleted)
	counts := ""
	if sourceAfter >= 0 {
		counts += fmt.Sprintf(" Source count: %d -> %d.", n, sourceAfter)
	}
	if deletedBefore >= 0 && deletedAfter >= 0 {
		counts += fmt.Sprintf(" Trash count: %d -> %d.", deletedBefore, deletedAfter)
	}
	syncNote := requestOutlookSync(ns)
	return fmt.Sprintf("Moved %d matching message(s) from %s to %s.%s%s%s", moved, sourceName, deletedName, more, counts, syncNote), nil
}

// outlookPurge permanently deletes explicitly filtered mail that is already in
// Outlook's configured Deleted Items folder. It never accepts another source
// folder, so the destructive operation cannot skip the recoverable Trash step.
func outlookPurge(ctx context.Context, ns *ole.IDispatch, a outlookArgs) (string, error) {
	if !a.Confirm {
		return "", fmt.Errorf("purge: confirm:true is required after explicit user approval for permanent deletion")
	}
	if strings.TrimSpace(a.From) == "" && strings.TrimSpace(a.Subject) == "" &&
		strings.TrimSpace(a.Since) == "" && strings.TrimSpace(a.Until) == "" {
		return "", fmt.Errorf("purge: pass at least one filter (from/subject/since/until); refusing unfiltered permanent deletion")
	}

	var since, until time.Time
	var err error
	if a.Since != "" {
		if since, err = time.ParseInLocation("2006-01-02", a.Since, time.Local); err != nil {
			return "", fmt.Errorf("purge: bad since date %q (want YYYY-MM-DD)", a.Since)
		}
	}
	if a.Until != "" {
		if until, err = time.ParseInLocation("2006-01-02", a.Until, time.Local); err != nil {
			return "", fmt.Errorf("purge: bad until date %q (want YYYY-MM-DD)", a.Until)
		}
		until = until.Add(24*time.Hour - time.Second)
	}

	deletedV, err := oleutil.CallMethod(ns, "GetDefaultFolder", olFolderDeletedItems)
	if err != nil {
		return "", fmt.Errorf("purge: GetDefaultFolder(Deleted Items): %w", err)
	}
	deleted := deletedV.ToIDispatch()
	defer deleted.Release()
	itemsV, err := oleutil.GetProperty(deleted, "Items")
	if err != nil {
		return "", fmt.Errorf("purge: Items: %w", err)
	}
	items := itemsV.ToIDispatch()
	defer items.Release()
	countV, err := oleutil.GetProperty(items, "Count")
	if err != nil {
		return "", fmt.Errorf("purge: Count: %w", err)
	}
	n := int(toInt64(countV.Value()))
	countV.Clear()

	fromNeedle := strings.ToLower(strings.TrimSpace(a.From))
	subjectNeedle := strings.ToLower(strings.TrimSpace(a.Subject))
	deletedCount := 0
	failed := 0
	for i := n; i >= 1 && deletedCount < a.Count; i-- {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		iv, err := oleutil.GetProperty(items, "Item", i)
		if err != nil {
			continue
		}
		item := iv.ToIDispatch()
		if cls := prop(item, "MessageClass"); cls != "" && !strings.HasPrefix(cls, "IPM.Note") {
			item.Release()
			continue
		}
		ok := true
		if fromNeedle != "" {
			name := strings.ToLower(prop(item, "SenderName"))
			addr := strings.ToLower(prop(item, "SenderEmailAddress"))
			ok = strings.Contains(name, fromNeedle) || strings.Contains(addr, fromNeedle)
		}
		if ok && subjectNeedle != "" {
			ok = strings.Contains(strings.ToLower(prop(item, "Subject")), subjectNeedle)
		}
		if ok && (!since.IsZero() || !until.IsZero()) {
			recv := propTime(item, "ReceivedTime")
			if !since.IsZero() && (recv.IsZero() || recv.Before(since)) {
				ok = false
			}
			if ok && !until.IsZero() && (recv.IsZero() || recv.After(until)) {
				ok = false
			}
		}
		if !ok {
			item.Release()
			continue
		}
		deletedV, deleteErr := oleutil.CallMethod(item, "Delete")
		item.Release()
		if deletedV != nil {
			deletedV.Clear()
		}
		if deleteErr != nil {
			failed++
			continue
		}
		deletedCount++
	}

	folderName := prop(deleted, "Name")
	after := outlookFolderItemCount(deleted)
	more := ""
	if deletedCount == a.Count {
		more = " The per-call safety limit was reached; repeat the same confirmed purge only if more matching messages should be permanently deleted."
	}
	if failed > 0 {
		more += fmt.Sprintf(" %d matching message(s) could not be permanently deleted.", failed)
	}
	counts := ""
	if after >= 0 {
		counts = fmt.Sprintf(" Trash count: %d -> %d.", n, after)
	}
	syncNote := requestOutlookSync(ns)
	return fmt.Sprintf("Permanently deleted %d matching message(s) from %s. This cannot be undone.%s%s%s", deletedCount, folderName, more, counts, syncNote), nil
}

func requestOutlookSync(ns *ole.IDispatch) string {
	v, err := oleutil.CallMethod(ns, "SendAndReceive", false)
	if v != nil {
		v.Clear()
	}
	if err != nil {
		return fmt.Sprintf(" Outlook local state changed, but the Send/Receive sync request failed: %v. Remote server state is NOT verified.%s", err, outlookSyncWarning(ns))
	}
	return " Outlook Send/Receive was requested asynchronously. Local Outlook state changed; remote server state is NOT yet verified." + outlookSyncWarning(ns)
}

func outlookFolderItemCount(folder *ole.IDispatch) int {
	itemsV, err := oleutil.GetProperty(folder, "Items")
	if err != nil {
		return -1
	}
	items := itemsV.ToIDispatch()
	defer items.Release()
	countV, err := oleutil.GetProperty(items, "Count")
	if err != nil {
		return -1
	}
	defer countV.Clear()
	return int(toInt64(countV.Value()))
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
