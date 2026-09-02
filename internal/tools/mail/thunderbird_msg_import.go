package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type thunderbirdFileTransfer struct {
	Path      string
	Name      string
	CreatedAt time.Time
}

func (b *thunderbirdBridgeState) registerMessageTransfer(path, name string) string {
	id := fmt.Sprintf("msg-%d-%d", time.Now().UnixNano(), b.requestID.Add(1))
	b.mu.Lock()
	b.transfers[id] = thunderbirdFileTransfer{Path: path, Name: name, CreatedAt: time.Now()}
	for key, transfer := range b.transfers {
		if time.Since(transfer.CreatedAt) > 10*time.Minute {
			delete(b.transfers, key)
		}
	}
	b.mu.Unlock()
	return id
}

func (b *thunderbirdBridgeState) removeMessageTransfer(id string) {
	b.mu.Lock()
	delete(b.transfers, id)
	b.mu.Unlock()
}

func (b *thunderbirdBridgeState) handleMessageFile(w http.ResponseWriter, r *http.Request) {
	setThunderbirdCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet || !b.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "missing transfer id", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	transfer, ok := b.transfers[id]
	b.mu.Unlock()
	if !ok {
		http.Error(w, "unknown or expired transfer", http.StatusNotFound)
		return
	}
	file, err := os.Open(transfer.Path)
	if err != nil {
		http.Error(w, "transfer file unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		http.Error(w, "transfer file unavailable", http.StatusNotFound)
		return
	}
	name := strings.TrimSpace(transfer.Name)
	if name == "" {
		name = "message.eml"
	}
	if !strings.EqualFold(filepath.Ext(name), ".eml") {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".eml"
	}
	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(w, r, name, stat.ModTime(), file)
}

func (t *ThunderbirdMail) importMSG(ctx context.Context, args thunderbirdToolArgs) (Result, error) {
	source := strings.TrimSpace(args.Path)
	if abs, err := filepath.Abs(source); err == nil {
		source = abs
	}
	if !strings.EqualFold(filepath.Ext(source), ".msg") {
		return Result{Err: fmt.Errorf("thunderbird_mail: import_msg supports only .msg files: %s", source)}, nil
	}
	stat, err := os.Stat(source)
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: cannot open .msg file %q: %w", source, err)}, nil
	}
	if !stat.Mode().IsRegular() {
		return Result{Err: fmt.Errorf("thunderbird_mail: import_msg path is not a regular file: %s", source)}, nil
	}

	tmp, err := os.CreateTemp("", "supercli-msg-import-*.eml")
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: cannot create temporary .eml: %w", err)}, nil
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return Result{Err: fmt.Errorf("thunderbird_mail: cannot prepare temporary .eml: %w", err)}, nil
	}
	defer os.Remove(tmpPath)

	meta, err := convertOutlookMSGToEML(ctx, source, tmpPath)
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: .msg -> .eml conversion failed: %w", err)}, nil
	}

	emlName := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)) + ".eml"
	transferID := globalThunderbirdBridge.registerMessageTransfer(tmpPath, emlName)
	defer globalThunderbirdBridge.removeMessageTransfer(transferID)

	bridgeArgs := map[string]any{
		"account":     args.Account,
		"destination": args.Destination,
		"confirm":     true,
		"transfer_id": transferID,
		"source_name": filepath.Base(source),
		"eml_name":    emlName,
	}
	raw, err := json.Marshal(bridgeArgs)
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: cannot prepare import request: %w", err)}, nil
	}
	data, err := globalThunderbirdBridge.call(ctx, "import_msg", raw)
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: %w", err)}, nil
	}

	var payload map[string]any
	if json.Unmarshal(data, &payload) == nil {
		payload["conversion"] = meta
		payload["sourcePath"] = source
		if formatted, err := json.MarshalIndent(payload, "", "  "); err == nil {
			return Result{Text: string(formatted)}, nil
		}
	}
	return Result{Text: string(data)}, nil
}
