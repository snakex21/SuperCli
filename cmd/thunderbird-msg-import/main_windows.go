//go:build windows

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"supercli/internal/tools"
)

const messageIDSchema = "http://schemas.microsoft.com/mapi/proptag/0x1035001F"

type candidate struct {
	Path      string
	Key       string
	MessageID string
	Category  string
	Folder    string
	Size      int64
}

type stateEntry struct {
	Key        string    `json:"key"`
	Path       string    `json:"path"`
	Folder     string    `json:"folder"`
	ImportedAt time.Time `json:"importedAt"`
}

type folderInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type foldersResponse struct {
	Folders []folderInfo `json:"folders"`
}

type createResponse struct {
	Created folderInfo `json:"created"`
}

func main() {
	source := flag.String("source", "", "root containing Outlook .msg backups")
	statePath := flag.String("state", "", "portable append-only progress file")
	archiveName := flag.String("archive", "Archiwum Outlook taty", "root Local Folders archive name")
	limit := flag.Int("limit", 0, "maximum new messages to import; 0 imports all")
	dryRun := flag.Bool("dry-run", false, "scan and report without changing Thunderbird")
	flag.Parse()

	if strings.TrimSpace(*source) == "" || strings.TrimSpace(*statePath) == "" {
		fatalf("-source and -state are required")
	}
	absSource, err := filepath.Abs(*source)
	if err != nil {
		fatalf("resolve source: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*statePath), 0o755); err != nil {
		fatalf("create state directory: %v", err)
	}

	completed, err := loadState(*statePath)
	if err != nil {
		fatalf("load state: %v", err)
	}
	fmt.Printf("SCAN source=%s completed=%d\n", absSource, len(completed))
	items, stats, err := scanCandidates(absSource)
	if err != nil {
		fatalf("scan .msg: %v", err)
	}
	pending := make([]candidate, 0, len(items))
	for _, item := range items {
		if _, ok := completed[item.Key]; !ok {
			pending = append(pending, item)
		}
	}
	fmt.Printf("PLAN files=%d unique=%d duplicateCopies=%d noMessageId=%d pending=%d uniqueGiB=%.3f\n",
		stats.Files, len(items), stats.Duplicates, stats.NoMessageID, len(pending), float64(stats.UniqueBytes)/(1<<30))
	for _, folder := range orderedFolders() {
		count := 0
		for _, item := range pending {
			if item.Folder == folder {
				count++
			}
		}
		fmt.Printf("PLAN_FOLDER folder=%q pending=%d\n", folder, count)
	}
	if *dryRun || len(pending) == 0 {
		return
	}

	ctx := context.Background()
	tool := tools.NewThunderbirdMail().Spec()
	status, err := callTool(ctx, tool.Fn, map[string]any{"op": "status"})
	if err != nil {
		fatalf("Thunderbird Bridge status: %v", err)
	}
	fmt.Printf("BRIDGE %s\n", compactJSON(status))

	destinations, err := ensureFolders(ctx, tool.Fn, *archiveName)
	if err != nil {
		fatalf("prepare Local Folders archive: %v", err)
	}
	state, err := os.OpenFile(*statePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fatalf("open state: %v", err)
	}
	defer state.Close()
	encoder := json.NewEncoder(state)

	started := time.Now()
	imported := 0
	failed := 0
	for _, item := range pending {
		if *limit > 0 && imported >= *limit {
			break
		}
		destination := destinations[item.Folder]
		_, err := callTool(ctx, tool.Fn, map[string]any{
			"op":          "import_msg",
			"account":     "Local Folders",
			"path":        item.Path,
			"destination": destination.ID,
			"confirm":     true,
		})
		if err != nil {
			failed++
			fmt.Printf("ERROR folder=%q path=%q error=%q\n", item.Folder, item.Path, err)
			continue
		}
		entry := stateEntry{Key: item.Key, Path: item.Path, Folder: item.Folder, ImportedAt: time.Now()}
		if err := encoder.Encode(entry); err != nil {
			fatalf("append state after importing %q: %v", item.Path, err)
		}
		if err := state.Sync(); err != nil {
			fatalf("flush state after importing %q: %v", item.Path, err)
		}
		imported++
		if imported == 1 || imported%10 == 0 {
			elapsed := time.Since(started)
			rate := float64(imported) / elapsed.Seconds()
			remaining := len(pending) - imported
			eta := time.Duration(0)
			if rate > 0 {
				eta = time.Duration(float64(remaining)/rate) * time.Second
			}
			fmt.Printf("PROGRESS imported=%d failed=%d pendingAfter=%d rate=%.2f/s elapsed=%s eta=%s\n",
				imported, failed, remaining, rate, elapsed.Round(time.Second), eta.Round(time.Second))
		}
	}
	fmt.Printf("DONE imported=%d failed=%d elapsed=%s state=%s\n", imported, failed, time.Since(started).Round(time.Second), *statePath)
	if failed > 0 {
		os.Exit(2)
	}
}

type scanStats struct {
	Files       int
	Duplicates  int
	NoMessageID int
	UniqueBytes int64
}

func scanCandidates(root string) ([]candidate, scanStats, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".msg") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, scanStats{}, err
	}
	sort.Strings(paths)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitialize(0); err != nil {
		if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 1 {
			return nil, scanStats{}, fmt.Errorf("COM init: %w", err)
		}
	}
	defer ole.CoUninitialize()
	unknown, err := oleutil.CreateObject("Outlook.Application")
	if err != nil {
		return nil, scanStats{}, fmt.Errorf("start Outlook: %w", err)
	}
	defer unknown.Release()
	app, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, scanStats{}, fmt.Errorf("Outlook dispatch: %w", err)
	}
	defer app.Release()
	nsRaw, err := oleutil.CallMethod(app, "GetNamespace", "MAPI")
	if err != nil {
		return nil, scanStats{}, fmt.Errorf("Outlook GetNamespace(MAPI): %w", err)
	}
	ns := nsRaw.ToIDispatch()
	defer ns.Release()

	byKey := make(map[string]candidate, len(paths))
	stats := scanStats{Files: len(paths)}
	for index, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, stats, statErr
		}
		messageID, readErr := outlookMessageID(ns, path)
		if readErr != nil {
			return nil, stats, fmt.Errorf("read Message-ID from %q: %w", path, readErr)
		}
		rel, _ := filepath.Rel(root, path)
		category := categoryFromRelative(rel)
		key := "mid:" + strings.ToLower(strings.TrimSpace(messageID))
		if strings.TrimSpace(messageID) == "" {
			stats.NoMessageID++
			hash := sha256.Sum256([]byte(strings.ToLower(rel)))
			key = "path:" + hex.EncodeToString(hash[:])
		}
		item := candidate{Path: path, Key: key, MessageID: messageID, Category: category, Folder: categoryFolder(category), Size: info.Size()}
		if previous, exists := byKey[key]; exists {
			stats.Duplicates++
			if categoryPriority(item.Category) < categoryPriority(previous.Category) {
				byKey[key] = item
			}
		} else {
			byKey[key] = item
		}
		if (index+1)%500 == 0 {
			fmt.Printf("SCAN_PROGRESS files=%d/%d\n", index+1, len(paths))
		}
	}
	items := make([]candidate, 0, len(byKey))
	for _, item := range byKey {
		items = append(items, item)
		stats.UniqueBytes += item.Size
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Folder != items[j].Folder {
			return folderPriority(items[i].Folder) < folderPriority(items[j].Folder)
		}
		return strings.ToLower(items[i].Path) < strings.ToLower(items[j].Path)
	})
	return items, stats, nil
}

func outlookMessageID(ns *ole.IDispatch, path string) (string, error) {
	itemRaw, err := oleutil.CallMethod(ns, "OpenSharedItem", path)
	if err != nil {
		return "", err
	}
	item := itemRaw.ToIDispatch()
	if item == nil {
		return "", fmt.Errorf("Outlook returned no item")
	}
	defer item.Release()
	accessorRaw, err := oleutil.GetProperty(item, "PropertyAccessor")
	if err != nil {
		return "", nil
	}
	accessor := accessorRaw.ToIDispatch()
	if accessor == nil {
		return "", nil
	}
	defer accessor.Release()
	value, err := oleutil.CallMethod(accessor, "GetProperty", messageIDSchema)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(fmt.Sprint(value.Value())), nil
}

func categoryFromRelative(rel string) string {
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) >= 2 {
		return parts[1]
	}
	return "Pozostałe"
}

func categoryFolder(category string) string {
	lower := strings.ToLower(category)
	switch {
	case strings.Contains(lower, "skrzynka odbiorcza"):
		return "Odebrane"
	case strings.Contains(lower, "wysłane") || strings.Contains(lower, "wyslane"):
		return "Wysłane"
	case strings.Contains(lower, "ważne") || strings.Contains(lower, "wazne"):
		return "Ważne"
	case strings.Contains(lower, "wersje robocze"):
		return "Wersje robocze"
	default:
		return "Pozostałe"
	}
}

func categoryPriority(category string) int { return folderPriority(categoryFolder(category)) }

func folderPriority(folder string) int {
	for i, name := range orderedFolders() {
		if name == folder {
			return i
		}
	}
	return 99
}

func orderedFolders() []string {
	return []string{"Odebrane", "Wysłane", "Ważne", "Wersje robocze", "Pozostałe"}
}

func ensureFolders(ctx context.Context, fn func(context.Context, json.RawMessage) (tools.Result, error), archiveName string) (map[string]folderInfo, error) {
	foldersRaw, err := callTool(ctx, fn, map[string]any{"op": "folders", "account": "Local Folders"})
	if err != nil {
		return nil, err
	}
	var listing foldersResponse
	if err := json.Unmarshal(foldersRaw, &listing); err != nil {
		return nil, err
	}
	find := func(name string, parentPath string) (folderInfo, bool) {
		for _, folder := range listing.Folders {
			if !strings.EqualFold(folder.Name, name) {
				continue
			}
			if parentPath == "" || strings.HasPrefix(strings.ToLower(folder.Path), strings.ToLower(strings.TrimRight(parentPath, "/")+"/")) {
				return folder, true
			}
		}
		return folderInfo{}, false
	}
	archive, ok := find(archiveName, "")
	if !ok {
		createdRaw, createErr := callTool(ctx, fn, map[string]any{"op": "create_folder", "account": "Local Folders", "name": archiveName, "confirm": true})
		if createErr != nil {
			return nil, createErr
		}
		var created createResponse
		if err := json.Unmarshal(createdRaw, &created); err != nil {
			return nil, err
		}
		archive = created.Created
		listing.Folders = append(listing.Folders, archive)
	}
	result := make(map[string]folderInfo)
	for _, name := range orderedFolders() {
		folder, exists := find(name, archive.Path)
		if !exists {
			createdRaw, createErr := callTool(ctx, fn, map[string]any{"op": "create_folder", "account": "Local Folders", "parent": archive.ID, "name": name, "confirm": true})
			if createErr != nil {
				return nil, createErr
			}
			var created createResponse
			if err := json.Unmarshal(createdRaw, &created); err != nil {
				return nil, err
			}
			folder = created.Created
			listing.Folders = append(listing.Folders, folder)
		}
		result[name] = folder
		fmt.Printf("DESTINATION folder=%q id=%q path=%q\n", name, folder.ID, folder.Path)
	}
	return result, nil
}

func callTool(ctx context.Context, fn func(context.Context, json.RawMessage) (tools.Result, error), args map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	result, err := fn(ctx, raw)
	if err != nil {
		return nil, err
	}
	if result.Err != nil {
		return nil, result.Err
	}
	return json.RawMessage(result.Text), nil
}

func loadState(path string) (map[string]stateEntry, error) {
	completed := make(map[string]stateEntry)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return completed, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)
	for scanner.Scan() {
		var entry stateEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, fmt.Errorf("bad state line: %w", err)
		}
		if entry.Key != "" {
			completed[entry.Key] = entry
		}
	}
	return completed, scanner.Err()
}

func compactJSON(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	compact, _ := json.Marshal(value)
	return string(compact)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL "+format+"\n", args...)
	os.Exit(1)
}
