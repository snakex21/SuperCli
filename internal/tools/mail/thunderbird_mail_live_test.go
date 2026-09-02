package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestThunderbirdBridgeLive(t *testing.T) {
	if os.Getenv("SUPERCLI_THUNDERBIRD_LIVE") != "1" {
		t.Skip("set SUPERCLI_THUNDERBIRD_LIVE=1 to run")
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	run := func(raw string) string {
		var args json.RawMessage = []byte(raw)
		res, err := tool.execute(ctx, args)
		if err != nil {
			t.Fatalf("execute(%s): %v", raw, err)
		}
		if res.Err != nil {
			t.Fatalf("tool(%s): %v", raw, res.Err)
		}
		fmt.Printf("\n%s =>\n%s\n", raw, res.Text)
		return res.Text
	}

	run(`{"op":"status"}`)
	run(`{"op":"accounts"}`)
	run(`{"op":"count","from":"Yanosik"}`)
	run(`{"op":"search","from":"Yanosik","limit":5}`)
	run(`{"op":"senders","limit":20}`)
	if os.Getenv("SUPERCLI_THUNDERBIRD_STRESS_SEARCH") == "1" {
		run(`{"op":"count","text":"newsletter"}`)
		run(`{"op":"status"}`)
	}
	if os.Getenv("SUPERCLI_THUNDERBIRD_VERIFY_PURGE_COUNTS") == "1" {
		run(`{"op":"count","folder":"Kosz","from":"support@indiegala.com"}`)
		run(`{"op":"count","folder":"Kosz","from":"newsletter@email2.gog.com"}`)
		run(`{"op":"count","folder":"Kosz","from":"xboxreps@engage.xbox.com"}`)
		run(`{"op":"count","folder":"Kosz","from":"info@email.sygic.com"}`)
		run(`{"op":"count","folder":"Kosz","from":"contact@mailer.humblebundle.com"}`)
	}
	if os.Getenv("SUPERCLI_THUNDERBIRD_RESTORE_DRYRUN") == "1" {
		restored := run(`{"op":"restore","from":"Yanosik","confirm":true,"batch_size":10,"dry_run":true}`)
		var batch struct {
			WouldProcess int  `json:"wouldProcess"`
			DryRun       bool `json:"dryRun"`
			Source       struct {
				Path string `json:"path"`
			} `json:"source"`
			Destination struct {
				Path string `json:"path"`
			} `json:"destination"`
		}
		if err := json.Unmarshal([]byte(restored), &batch); err != nil {
			t.Fatalf("parse restore dry-run: %v", err)
		}
		if !batch.DryRun || batch.Source.Path == "" || batch.Destination.Path == "" {
			t.Fatalf("unexpected restore dry-run result: %+v", batch)
		}
	}
	if os.Getenv("SUPERCLI_THUNDERBIRD_BULK_DRYRUN") == "1" {
		first := run(`{"op":"trash","folder":"Odebrane","from":"Yanosik","confirm":true,"batch_size":10,"dry_run":true}`)
		var batch struct {
			WouldProcess int    `json:"wouldProcess"`
			More         bool   `json:"more"`
			Continuation string `json:"continuation"`
			DryRun       bool   `json:"dryRun"`
		}
		if err := json.Unmarshal([]byte(first), &batch); err != nil {
			t.Fatalf("parse dry-run batch: %v", err)
		}
		if batch.WouldProcess != 10 || !batch.More || batch.Continuation == "" || !batch.DryRun {
			t.Fatalf("unexpected bulk dry-run result: %+v", batch)
		}
		run(fmt.Sprintf(`{"op":"trash","folder":"Odebrane","from":"Yanosik","confirm":true,"batch_size":10,"continuation":%q,"dry_run":true}`, batch.Continuation))
	}
}

func TestThunderbirdFolderCRUDLive(t *testing.T) {
	if os.Getenv("SUPERCLI_THUNDERBIRD_FOLDER_CRUD") != "1" {
		t.Skip("set SUPERCLI_THUNDERBIRD_FOLDER_CRUD=1 to run")
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run := func(raw string) string {
		t.Helper()
		res, err := tool.execute(ctx, json.RawMessage(raw))
		if err != nil {
			t.Fatalf("execute(%s): %v", raw, err)
		}
		if res.Err != nil {
			t.Fatalf("tool(%s): %v", raw, res.Err)
		}
		return res.Text
	}

	name := fmt.Sprintf("SuperCLI-CRUD-%d", time.Now().UnixNano())
	renamedName := name + "-renamed"
	createdRaw := run(fmt.Sprintf(`{"op":"create_folder","name":%q,"confirm":true}`, name))
	var created struct {
		Created struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"created"`
	}
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		t.Fatalf("parse create result: %v: %s", err, createdRaw)
	}
	if created.Created.ID == "" || created.Created.Name != name {
		t.Fatalf("unexpected create result: %s", createdRaw)
	}

	renamedRaw := run(fmt.Sprintf(`{"op":"rename_folder","folder":%q,"new_name":%q,"confirm":true}`, created.Created.ID, renamedName))
	var renamed struct {
		Renamed struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"renamed"`
	}
	if err := json.Unmarshal([]byte(renamedRaw), &renamed); err != nil {
		t.Fatalf("parse rename result: %v: %s", err, renamedRaw)
	}
	if renamed.Renamed.ID == "" || renamed.Renamed.Name != renamedName {
		t.Fatalf("unexpected rename result: %s", renamedRaw)
	}

	deletedRaw := run(fmt.Sprintf(`{"op":"delete_folder","folder":%q,"confirm":true}`, renamed.Renamed.ID))
	var deleted struct {
		Deleted bool `json:"deleted"`
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal([]byte(deletedRaw), &deleted); err != nil {
		t.Fatalf("parse delete result: %v: %s", err, deletedRaw)
	}
	if !deleted.Deleted || !deleted.Changed {
		t.Fatalf("folder deletion was not verified: %s", deletedRaw)
	}
}

func TestThunderbirdFoldersLive(t *testing.T) {
	if os.Getenv("SUPERCLI_THUNDERBIRD_FOLDERS") != "1" {
		t.Skip("set SUPERCLI_THUNDERBIRD_FOLDERS=1 to run")
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := tool.execute(ctx, json.RawMessage(`{"op":"folders"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	fmt.Println(res.Text)
}

func TestThunderbirdRenameFolderLive(t *testing.T) {
	folder := os.Getenv("SUPERCLI_THUNDERBIRD_RENAME_FOLDER")
	newName := os.Getenv("SUPERCLI_THUNDERBIRD_RENAME_TO")
	if folder == "" || newName == "" {
		t.Skip("set SUPERCLI_THUNDERBIRD_RENAME_FOLDER and SUPERCLI_THUNDERBIRD_RENAME_TO to run")
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	raw := fmt.Sprintf(`{"op":"rename_folder","folder":%q,"new_name":%q,"confirm":true}`, folder, newName)
	res, err := tool.execute(ctx, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	fmt.Println(res.Text)
}

func TestThunderbirdDeleteFolderLive(t *testing.T) {
	folder := os.Getenv("SUPERCLI_THUNDERBIRD_DELETE_FOLDER")
	if folder == "" {
		t.Skip("set SUPERCLI_THUNDERBIRD_DELETE_FOLDER to run")
	}
	if !strings.HasPrefix(strings.TrimPrefix(folder, "/"), "SuperCLI-CRUD-") {
		t.Fatalf("refusing to delete non-test folder %q", folder)
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	raw := fmt.Sprintf(`{"op":"delete_folder","folder":%q,"confirm":true}`, folder)
	res, err := tool.execute(ctx, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	fmt.Println(res.Text)
}

func TestThunderbirdReadMessageLive(t *testing.T) {
	if os.Getenv("SUPERCLI_THUNDERBIRD_READ_LIVE") != "1" {
		t.Skip("set SUPERCLI_THUNDERBIRD_READ_LIVE=1 to run")
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	call := func(raw string) string {
		t.Helper()
		res, err := tool.execute(ctx, json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		return res.Text
	}

	var search struct {
		Messages []struct {
			ID int64 `json:"id"`
		} `json:"messages"`
	}
	searchRaw := call(`{"op":"search","limit":5}`)
	if err := json.Unmarshal([]byte(searchRaw), &search); err != nil {
		t.Fatalf("parse search result: %v", err)
	}
	if len(search.Messages) == 0 {
		t.Skip("Inbox has no message available for a read-only body test")
	}

	for _, candidate := range search.Messages {
		var read struct {
			Message struct {
				ID int64 `json:"id"`
			} `json:"message"`
			BodyAvailable    bool `json:"bodyAvailable"`
			ReturnedChars    int  `json:"returnedCharacters"`
			TotalChars       int  `json:"totalCharacters"`
			HasMore          bool `json:"hasMore"`
			NextStartChar    *int `json:"nextStartChar"`
			ReadStateChanged bool `json:"readStateChanged"`
		}
		readRaw := call(fmt.Sprintf(`{"op":"read","message_id":%d,"max_chars":500}`, candidate.ID))
		if err := json.Unmarshal([]byte(readRaw), &read); err != nil {
			t.Fatalf("parse read result: %v", err)
		}
		if !read.BodyAvailable {
			continue
		}
		if read.Message.ID != candidate.ID || read.ReturnedChars <= 0 || read.ReturnedChars > 500 {
			t.Fatalf("unexpected read metadata: %+v", read)
		}
		if read.ReadStateChanged {
			t.Fatal("read operation unexpectedly changed the message read state")
		}
		if read.HasMore && read.NextStartChar == nil {
			t.Fatal("paged body did not return nextStartChar")
		}
		if read.HasMore {
			var continued struct {
				ReturnedChars    int  `json:"returnedCharacters"`
				StartChar        int  `json:"startChar"`
				ReadStateChanged bool `json:"readStateChanged"`
			}
			continuedRaw := call(fmt.Sprintf(`{"op":"read","message_id":%d,"max_chars":500,"start_char":%d}`, candidate.ID, *read.NextStartChar))
			if err := json.Unmarshal([]byte(continuedRaw), &continued); err != nil {
				t.Fatalf("parse continued read result: %v", err)
			}
			if continued.StartChar != *read.NextStartChar || continued.ReturnedChars <= 0 || continued.ReadStateChanged {
				t.Fatalf("unexpected continued read metadata: %+v", continued)
			}
		}
		fmt.Printf("read message_id=%d returned=%d total=%d hasMore=%t stateUnchanged=true\n", candidate.ID, read.ReturnedChars, read.TotalChars, read.HasMore)
		return
	}
	t.Skip("No readable body among the five newest Inbox messages")
}

func TestThunderbirdLocalArchiveLive(t *testing.T) {
	if os.Getenv("SUPERCLI_THUNDERBIRD_LOCAL_ARCHIVE_LIVE") != "1" {
		t.Skip("set SUPERCLI_THUNDERBIRD_LOCAL_ARCHIVE_LIVE=1 to run")
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	call := func(raw string) string {
		t.Helper()
		res, err := tool.execute(ctx, json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		return res.Text
	}

	expected := map[string]int{
		"/Archiwum Outlook taty/Odebrane":       1335,
		"/Archiwum Outlook taty/Wysłane":        2532,
		"/Archiwum Outlook taty/Ważne":          915,
		"/Archiwum Outlook taty/Wersje robocze": 26,
		"/Archiwum Outlook taty/Pozostałe":      14,
	}
	for folder, want := range expected {
		raw := call(fmt.Sprintf(`{"op":"count","account":"Local Folders","folder":%q}`, folder))
		var result struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatalf("parse count for %s: %v", folder, err)
		}
		if result.Count != want {
			t.Fatalf("%s count=%d, want %d", folder, result.Count, want)
		}
	}

	searchRaw := call(`{"op":"search","account":"Local Folders","folder":"/Archiwum Outlook taty/Wysłane","subject":"Rabacja","limit":10}`)
	var search struct {
		Messages []struct {
			ID              int64 `json:"id"`
			AttachmentCount int   `json:"attachmentCount"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(searchRaw), &search); err != nil {
		t.Fatalf("parse Rabacja search: %v", err)
	}
	if len(search.Messages) == 0 {
		t.Fatal("imported Rabacja message was not found")
	}
	message := search.Messages[0]
	if message.AttachmentCount == 0 {
		t.Fatal("imported Rabacja message has no attachment metadata")
	}

	readRaw := call(fmt.Sprintf(`{"op":"read","message_id":%d,"max_chars":1000}`, message.ID))
	var read struct {
		BodyAvailable    bool `json:"bodyAvailable"`
		ReadStateChanged bool `json:"readStateChanged"`
	}
	if err := json.Unmarshal([]byte(readRaw), &read); err != nil {
		t.Fatalf("parse Rabacja body: %v", err)
	}
	if !read.BodyAvailable || read.ReadStateChanged {
		t.Fatalf("unexpected Rabacja body metadata: %+v", read)
	}

	attachmentsRaw := call(fmt.Sprintf(`{"op":"attachments","message_id":%d}`, message.ID))
	if !strings.Contains(strings.ToLower(attachmentsRaw), ".pdf") {
		t.Fatalf("Rabacja PDF attachment metadata missing: %s", attachmentsRaw)
	}
	fmt.Printf("local archive verified: messages=4822 Rabacja_id=%d attachmentCount=%d bodyAvailable=true\n", message.ID, message.AttachmentCount)
}

func TestThunderbirdCompactFolderLive(t *testing.T) {
	folder := os.Getenv("SUPERCLI_THUNDERBIRD_COMPACT_FOLDER")
	if folder == "" {
		t.Skip("set SUPERCLI_THUNDERBIRD_COMPACT_FOLDER to run")
	}
	tool := NewThunderbirdMail()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	call := func(raw string) string {
		t.Helper()
		res, err := tool.execute(ctx, json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		return res.Text
	}
	count := func() int {
		var result struct {
			Count int `json:"count"`
		}
		raw := call(fmt.Sprintf(`{"op":"count","folder":%q,"scope":"account"}`, folder))
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatalf("parse count result: %v", err)
		}
		return result.Count
	}

	beforeCount := count()
	var compacted struct {
		BeforeBytes     int64 `json:"beforeBytes"`
		AfterBytes      int64 `json:"afterBytes"`
		ReclaimedBytes  int64 `json:"reclaimedBytes"`
		Compacted       bool  `json:"compacted"`
		MessagesChanged bool  `json:"messagesChanged"`
	}
	compactRaw := call(fmt.Sprintf(`{"op":"compact_folder","folder":%q,"confirm":true}`, folder))
	if err := json.Unmarshal([]byte(compactRaw), &compacted); err != nil {
		t.Fatalf("parse compact result: %v", err)
	}
	afterCount := count()
	if beforeCount != afterCount {
		t.Fatalf("message count changed during compaction: before=%d after=%d", beforeCount, afterCount)
	}
	if !compacted.Compacted || compacted.MessagesChanged {
		t.Fatalf("unexpected compact result: %+v", compacted)
	}
	fmt.Printf("folder=%s messages=%d beforeBytes=%d afterBytes=%d reclaimedBytes=%d\n", folder, afterCount, compacted.BeforeBytes, compacted.AfterBytes, compacted.ReclaimedBytes)
}
