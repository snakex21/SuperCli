package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThunderbirdMailMutationGuards(t *testing.T) {
	tool := NewThunderbirdMail()

	cases := []struct {
		raw  string
		want string
	}{
		{`{"op":"trash","from":"yanosik"}`, "confirm:true"},
		{`{"op":"trash","confirm":true}`, "requires at least one filter"},
		{`{"op":"restore","from":"yanosik"}`, "confirm:true"},
		{`{"op":"restore","confirm":true}`, "requires at least one filter"},
		{`{"op":"move","folder":"Ważne","destination":"Odebrane","from":"yanosik"}`, "confirm:true"},
		{`{"op":"move","folder":"Ważne","confirm":true,"from":"yanosik"}`, "requires destination"},
		{`{"op":"move","folder":"Ważne","destination":"Odebrane","confirm":true}`, "requires at least one filter"},
		{`{"op":"import_msg","path":"C:\\stare\\mail.msg","destination":"Stare"}`, "confirm:true"},
		{`{"op":"import_msg","destination":"Stare","confirm":true}`, "requires path"},
		{`{"op":"import_msg","path":"C:\\stare\\mail.msg","confirm":true}`, "requires destination"},
		{`{"op":"purge","from":"yanosik"}`, "confirm:true"},
		{`{"op":"delete_permanently","confirm":true}`, "requires at least one filter"},
		{`{"op":"attachments"}`, "requires message_id"},
		{`{"op":"get_attachment"}`, "requires message_id"},
		{`{"op":"read"}`, "requires message_id"},
		{`{"op":"read","message_id":1,"max_chars":100}`, "max_chars must be between"},
		{`{"op":"read","message_id":1,"start_char":-1}`, "start_char cannot be negative"},
		{`{"op":"create_folder","name":"Test"}`, "confirm:true"},
		{`{"op":"create_folder","confirm":true}`, "requires name"},
		{`{"op":"create_address_book","name":"Kontakty"}`, "confirm:true"},
		{`{"op":"create_address_book","confirm":true}`, "requires name"},
		{`{"op":"add_contacts","address_book":"Kontakty","contacts":[{"name":"Jan","email":"jan@example.test"}]}`, "confirm:true"},
		{`{"op":"add_contacts","confirm":true,"contacts":[{"name":"Jan","email":"jan@example.test"}]}`, "requires address_book"},
		{`{"op":"add_contacts","confirm":true,"address_book":"Kontakty"}`, "requires 1-200 contacts"},
		{`{"op":"update_contact","contact_id":"contact-1","name":"Jan"}`, "confirm:true"},
		{`{"op":"update_contact","confirm":true,"name":"Jan"}`, "requires contact_id"},
		{`{"op":"update_contact","confirm":true,"contact_id":"contact-1"}`, "requires name or email"},
		{`{"op":"rename_folder","folder":"Test","new_name":"Nowy"}`, "confirm:true"},
		{`{"op":"rename_folder","confirm":true,"folder":"Test"}`, "requires folder and new_name"},
		{`{"op":"delete_folder","folder":"Test"}`, "confirm:true"},
		{`{"op":"delete_folder","confirm":true}`, "requires folder"},
		{`{"op":"compact_folder","folder":"INBOX"}`, "confirm:true"},
		{`{"op":"compact_folder","confirm":true}`, "requires folder"},
		{`{"op":"empty_trash"}`, "confirm:true"},
	}

	for _, tc := range cases {
		res, err := tool.execute(context.Background(), json.RawMessage(tc.raw))
		if err != nil {
			t.Fatalf("execute(%s): %v", tc.raw, err)
		}
		if res.Err == nil || !strings.Contains(res.Err.Error(), tc.want) {
			t.Fatalf("execute(%s): got err=%v, want containing %q", tc.raw, res.Err, tc.want)
		}
	}
}

func TestThunderbirdMoveAcceptsExactMessageSelectors(t *testing.T) {
	cases := []struct {
		name string
		args thunderbirdToolArgs
	}{
		{name: "Thunderbird message ids", args: thunderbirdToolArgs{Confirm: true, Destination: "Archiwum", MessageIDs: []int64{123, 456}}},
		{name: "single RFC Message-ID", args: thunderbirdToolArgs{Confirm: true, Destination: "Archiwum", HeaderMessageID: "<mail-123@example.test>"}},
		{name: "multiple RFC Message-IDs", args: thunderbirdToolArgs{Confirm: true, Destination: "Archiwum", HeaderMessageIDs: []string{"<mail-123@example.test>", "<mail-456@example.test>"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateThunderbirdMoveArgs(tc.args); err != nil {
				t.Fatalf("exact message selector was rejected by move validation: %v", err)
			}
		})
	}
}

func TestThunderbirdMutationSelectorRejectsEmptyValues(t *testing.T) {
	args := thunderbirdToolArgs{
		MessageIDs:       []int64{},
		HeaderMessageID:  "  ",
		HeaderMessageIDs: []string{},
		From:             " ",
		Subject:          "\t",
		Text:             "\r\n",
		Since:            " ",
		Until:            " ",
	}
	if thunderbirdHasMutationSelector(args) {
		t.Fatal("empty selector values must not authorize a mutation")
	}
}

func TestThunderbirdFolderMutationsUseIMAPSafeTimeout(t *testing.T) {
	for _, op := range []string{"create_folder", "rename_folder", "delete_folder"} {
		if got := thunderbirdOperationTimeout(op); got != 120*time.Second {
			t.Errorf("%s timeout = %s, want 120s", op, got)
		}
	}
}

func TestThunderbirdAddressBookOperationsUseSafeTimeout(t *testing.T) {
	for _, op := range []string{"contact_candidates", "create_address_book", "add_contacts", "update_contact"} {
		if got := thunderbirdOperationTimeout(op); got != 120*time.Second {
			t.Errorf("%s timeout = %s, want 120s", op, got)
		}
	}
}

func TestThunderbirdReadUsesIMAPSafeTimeout(t *testing.T) {
	if got := thunderbirdOperationTimeout("read"); got != 120*time.Second {
		t.Errorf("read timeout = %s, want 120s", got)
	}
}

func TestThunderbirdCompactUsesMaintenanceTimeout(t *testing.T) {
	if got := thunderbirdOperationTimeout("compact_folder"); got != 15*time.Minute {
		t.Errorf("compact_folder timeout = %s, want 15m", got)
	}
}

func TestThunderbirdVisionMIME(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}, "image/png"},
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "image/jpeg"},
		{"gif", []byte("GIF89a......"), "image/gif"},
		{"webp", []byte("RIFF....WEBP"), "image/webp"},
		{"pdf", []byte("%PDF-1.7...."), ""},
	}
	for _, tc := range cases {
		if got := thunderbirdVisionMIME(tc.data); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestThunderbirdAttachmentUploadEndpoint(t *testing.T) {
	state := &thunderbirdBridgeState{downloads: make(map[string]thunderbirdDownloadedAttachment)}
	body := []byte("hello attachment")
	req := httptest.NewRequest(http.MethodPost, "/attachment-file?token="+thunderbirdBridgeToken+"&id=att-endpoint&filename=hello.txt&content_type=text%2Fplain", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	state.handleAttachmentFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	item, ok := state.downloadedAttachment("att-endpoint")
	if !ok {
		t.Fatal("downloaded attachment was not registered")
	}
	defer os.Remove(item.Path)
	got, err := os.ReadFile(item.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) || item.Name != "hello.txt" || item.ContentType != "text/plain" {
		t.Fatalf("unexpected attachment: %+v body=%q", item, got)
	}
}

func TestThunderbirdAttachmentResultAttachesCommonImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	data := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	id := "att-unit-image"
	globalThunderbirdBridge.mu.Lock()
	globalThunderbirdBridge.downloads[id] = thunderbirdDownloadedAttachment{Path: path, Name: "photo.png", ContentType: "image/png", Size: int64(len(data)), CreatedAt: time.Now()}
	globalThunderbirdBridge.mu.Unlock()
	defer func() {
		globalThunderbirdBridge.mu.Lock()
		delete(globalThunderbirdBridge.downloads, id)
		globalThunderbirdBridge.mu.Unlock()
	}()

	res, err := NewThunderbirdMail().attachmentResult(json.RawMessage(`{"transferId":"att-unit-image","filename":"photo.png","contentType":"image/png","size":12,"partName":"1.2"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("attachmentResult: res=%+v err=%v", res, err)
	}
	if res.Image == nil || res.Image.MediaType != "image/png" || string(res.Image.Data) != string(data) {
		t.Fatalf("image not attached correctly: %+v", res.Image)
	}
	if !strings.Contains(res.Text, `"visionAttached": true`) || !strings.Contains(res.Text, `"filename": "photo.png"`) {
		t.Fatalf("unexpected text: %s", res.Text)
	}
}

func TestThunderbirdAttachmentResultLeavesPDFAsLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document.pdf")
	data := []byte("%PDF-1.7 test payload")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	id := "att-unit-pdf"
	globalThunderbirdBridge.mu.Lock()
	globalThunderbirdBridge.downloads[id] = thunderbirdDownloadedAttachment{Path: path, Name: "document.pdf", ContentType: "application/pdf", Size: int64(len(data)), CreatedAt: time.Now()}
	globalThunderbirdBridge.mu.Unlock()
	defer func() {
		globalThunderbirdBridge.mu.Lock()
		delete(globalThunderbirdBridge.downloads, id)
		globalThunderbirdBridge.mu.Unlock()
	}()

	res, err := NewThunderbirdMail().attachmentResult(json.RawMessage(`{"transferId":"att-unit-pdf","filename":"document.pdf","contentType":"application/pdf","size":21,"partName":"2"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("attachmentResult: res=%+v err=%v", res, err)
	}
	if res.Image != nil {
		t.Fatal("PDF must not be sent as an image")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Text), &payload); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if payload["localPath"] != path || payload["visionAttached"] != false {
		t.Fatalf("unexpected text: %s", res.Text)
	}
}
