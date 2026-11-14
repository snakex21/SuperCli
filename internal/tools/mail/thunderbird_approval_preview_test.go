package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	core "supercli/internal/tools/core"
)

// fakeThunderbirdPreview answers the read-only bridge operations an approval
// preview may use and records every operation it saw.
type fakeThunderbirdPreview struct {
	ops      []string
	messages map[int64]string // id -> plain message JSON
}

func (f *fakeThunderbirdPreview) call(_ context.Context, op string, raw json.RawMessage) (json.RawMessage, error) {
	f.ops = append(f.ops, op)
	switch op {
	case "accounts":
		return json.RawMessage(`[{"id":"account1","name":"Gmail (me@example.com)"},{"id":"account2","name":"Local Folders"}]`), nil
	case "attachments":
		var args struct {
			MessageID int64 `json:"message_id"`
		}
		_ = json.Unmarshal(raw, &args)
		m, ok := f.messages[args.MessageID]
		if !ok {
			return nil, fmt.Errorf("thunderbird bridge: Message not found: %d", args.MessageID)
		}
		return json.RawMessage(`{"message":` + m + `,"hasAttachments":false,"attachmentCount":0,"attachments":[]}`), nil
	case "count":
		return json.RawMessage(`{"count":0}`), nil
	}
	return nil, fmt.Errorf("unexpected op %s", op)
}

func tbMessage(id int64, subject, author, date, folderID, accountID, name, path string, special ...string) string {
	use, _ := json.Marshal(special)
	if special == nil {
		use = []byte("[]")
	}
	return fmt.Sprintf(`{"id":%d,"author":%q,"subject":%q,"date":%q,"folder":{"id":%q,"accountId":%q,"name":%q,"path":%q,"specialUse":%s}}`,
		id, author, subject, date, folderID, accountID, name, path, use)
}

func previewFor(t *testing.T, fake *fakeThunderbirdPreview, raw string) *core.ApprovalRequest {
	t.Helper()
	tool := NewThunderbirdMail()
	tool.previewCall = fake.call
	req, err := tool.approval(context.Background(), json.RawMessage(raw))
	if err != nil || req == nil || req.Preview == nil {
		t.Fatalf("approval(%s) = %+v, %v", raw, req, err)
	}
	req.ApplyPreview(req.Preview(context.Background()))
	return req
}

func TestThunderbirdPreviewResolvesMessageIDsInStatedFolder(t *testing.T) {
	fake := &fakeThunderbirdPreview{messages: map[int64]string{}}
	for i := int64(1); i <= 4; i++ {
		fake.messages[i] = tbMessage(i, fmt.Sprintf("Order %d", i), "Shop <shop@example.com>", "2026-11-0"+fmt.Sprint(i)+"T10:00:00.000Z", "account1://INBOX", "account1", "Inbox", "/INBOX", "inbox")
	}
	req := previewFor(t, fake, `{"op":"trash","account":"account1","folder":"INBOX","message_ids":[1,2,3,4]}`)
	if req.Count != 4 || len(req.Samples) != 3 || len(req.Warnings) != 0 {
		t.Fatalf("count=%d samples=%+v warnings=%v", req.Count, req.Samples, req.Warnings)
	}
	if s := req.Samples[0]; s.Subject != "Order 1" || s.From != "Shop <shop@example.com>" || s.Date != "2026-11-01" {
		t.Fatalf("sample %+v", s)
	}
	for _, op := range fake.ops {
		if op != "accounts" && op != "attachments" {
			t.Fatalf("preview used a non read-only op: %v", fake.ops)
		}
	}
	// The default source of trash is the Inbox.
	req = previewFor(t, fake, `{"op":"trash","message_ids":[2]}`)
	if len(req.Warnings) != 0 || len(req.Samples) != 1 {
		t.Fatalf("default inbox: %+v", req)
	}
}

func TestThunderbirdPreviewWarnsWhenMessageIDsAreElsewhere(t *testing.T) {
	fake := &fakeThunderbirdPreview{messages: map[int64]string{
		7: tbMessage(7, "Invoice", "a@example.com", "2026-11-01T00:00:00Z", "account1://[Gmail]/All Mail", "account1", "All Mail", "/[Gmail]/All Mail", "all"),
		8: tbMessage(8, "Hello", "b@example.com", "2026-11-02T00:00:00Z", "account1://INBOX", "account1", "Inbox", "/INBOX", "inbox"),
	}}
	req := previewFor(t, fake, `{"op":"move","account":"account1","folder":"INBOX","destination":"Archive","message_ids":[7,8,9]}`)
	if req.Count != 3 || len(req.Samples) != 2 || len(req.Warnings) != 2 {
		t.Fatalf("count=%d samples=%d warnings=%q", req.Count, len(req.Samples), req.Warnings)
	}
	if w := req.Warnings[0]; !strings.Contains(w, `Not in "INBOX"`) || !strings.Contains(w, `7 "Invoice" is in "/[Gmail]/All Mail" (Gmail (me@example.com))`) || strings.Contains(w, "8 ") {
		t.Fatalf("folder warning %q", w)
	}
	if w := req.Warnings[1]; !strings.Contains(w, "message id 9") {
		t.Fatalf("missing-id warning %q", w)
	}
	// restore reads from the default Trash.
	req = previewFor(t, fake, `{"op":"restore","account":"account1","message_ids":[8]}`)
	if len(req.Warnings) != 1 || !strings.Contains(req.Warnings[0], "Not in the default Trash") {
		t.Fatalf("restore warnings %q", req.Warnings)
	}
}

func TestThunderbirdPreviewChecksABoundedNumberOfIDs(t *testing.T) {
	fake := &fakeThunderbirdPreview{messages: map[int64]string{}}
	ids := make([]string, 0, 30)
	for i := 1; i <= 30; i++ {
		ids = append(ids, fmt.Sprint(i))
	}
	req := previewFor(t, fake, `{"op":"trash","folder":"INBOX","message_ids":[`+strings.Join(ids, ",")+`]}`)
	attachments := 0
	for _, op := range fake.ops {
		if op == "attachments" {
			attachments++
		}
	}
	if attachments != thunderbirdPreviewIDLimit || req.Count != 30 {
		t.Fatalf("looked up %d ids, count %d", attachments, req.Count)
	}
	if last := req.Warnings[len(req.Warnings)-1]; !strings.Contains(last, "first 10 of 30") {
		t.Fatalf("warnings %q", req.Warnings)
	}
}

// Live: the card said "Move matching messages ... in account2" although the
// account row showed the display name.
func TestThunderbirdPreviewSummaryUsesAccountDisplayNames(t *testing.T) {
	fake := &fakeThunderbirdPreview{}
	req := previewFor(t, fake, `{"op":"trash","account":"account2","folder":"account2://Inbox","subject":"x"}`)
	if req.AccountName != "Local Folders" || req.Summary != `Move matching messages from "Inbox" in Local Folders to Trash.` {
		t.Fatalf("summary %q (account %q)", req.Summary, req.AccountName)
	}
	req = previewFor(t, fake, `{"op":"copy","folder":"account1://INBOX","destination":"_SuperCliTest","destination_account":"account2","subject":"x"}`)
	if want := `Copy matching messages from "INBOX" in Gmail (me@example.com) to "_SuperCliTest" in Local Folders; the originals stay.`; req.Summary != want {
		t.Fatalf("copy summary %q", req.Summary)
	}
	if strings.Contains(req.Summary, "account1") || strings.Contains(req.Summary, "account2") {
		t.Fatalf("summary still names account ids: %q", req.Summary)
	}
	// Without the bridge the classifier's id-based summary stays.
	tool := NewThunderbirdMail()
	tool.previewCall = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, fmt.Errorf("Thunderbird Bridge is not connected")
	}
	plain, _ := tool.approval(context.Background(), json.RawMessage(`{"op":"trash","account":"account2","subject":"x"}`))
	original := plain.Summary
	plain.ApplyPreview(plain.Preview(context.Background()))
	if plain.Summary != original || !strings.Contains(original, "in account2") || len(plain.Warnings) != 0 {
		t.Fatalf("offline preview changed the request: %+v", plain)
	}
}
