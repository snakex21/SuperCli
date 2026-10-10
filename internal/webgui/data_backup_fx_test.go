package webgui

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/storage/session"
)

func portableFXBackupDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".tmp", "backup-fx-tests"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func syntheticFXBackupMultipliers(pln float64) map[string]float64 {
	values := make(map[string]float64)
	// The first version supported these ten currencies. Preserve compatibility
	// with its immutable snapshots while add-on quotes extend other currencies.
	for _, code := range []string{"USD", "PLN", "EUR", "GBP", "CHF", "JPY", "CAD", "CZK", "NOK", "SEK"} {
		values[code] = 2
	}
	values["USD"], values["PLN"] = 1, pln
	return values
}

// Keep this independent schema fixture in WAL mode until its caller closes it.
// A direct file copy would omit its committed row; VACUUM INTO must include it.
func createFXBackupFixture(t *testing.T, root string, pln float64) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, dataCurrencyRatesFile)+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE currency_days (
		id INTEGER PRIMARY KEY AUTOINCREMENT, usage_day TEXT NOT NULL UNIQUE,
		publication_day TEXT NOT NULL, source TEXT NOT NULL, multipliers TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(syntheticFXBackupMultipliers(pln))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO currency_days(usage_day,publication_day,source,multipliers)
		VALUES('2020-02-03','2020-02-03','NBP table A',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE currency_quotes (
		id INTEGER PRIMARY KEY AUTOINCREMENT, usage_day TEXT NOT NULL, currency TEXT NOT NULL,
		multiplier REAL NOT NULL, publication_day TEXT NOT NULL, usd_publication_day TEXT NOT NULL,
		source TEXT NOT NULL, UNIQUE(usage_day,currency))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO currency_quotes(usage_day,currency,multiplier,publication_day,usd_publication_day,source)
		VALUES('2020-02-03','AED',2,'2020-01-29','2020-02-03','NBP table B / USD table A')`); err != nil {
		t.Fatal(err)
	}
	return db
}

type rejectFXBackupHTTP struct{ t *testing.T }

func (r rejectFXBackupHTTP) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("backup restore attempted HTTP: %s", req.URL)
	return nil, fmt.Errorf("network forbidden in backup fixture")
}

func TestFXBackupRestoresDeletedBillingHistoryAndCommittedWALRates(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprintf("full=%t", full), func(t *testing.T) {
			source, target := portableFXBackupDir(t), portableFXBackupDir(t)
			_ = createFXBackupFixture(t, source, 4)
			if info, err := os.Stat(filepath.Join(source, dataCurrencyRatesFile+"-wal")); err != nil || info.Size() == 0 {
				t.Fatalf("fixture did not leave committed rates in WAL: %v", err)
			}
			store, err := session.OpenStore(source)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			sess, err := store.Create(source, "fixture", "already deleted conversation")
			if err != nil {
				t.Fatal(err)
			}
			amount := 5.0
			price := &session.PriceSnapshot{State: "manual", AmountUSD: &amount, Source: "manual",
				InputPerMillion: 5, PriceDate: "2020-02-03", UsageDay: "2020-02-03"}
			if err := store.AppendUsage(context.Background(), session.UsageRecord{SessionID: sess.ID, Provider: "fixture", Model: "fixture",
				Input: 1000000, ContextSystem: 100, ContextTool: 200, HasTiming: true, DurationMS: 6000, TTFTMS: 2000,
				Source: "main", CreatedAt: time.Date(2020, 2, 3, 12, 0, 0, 0, time.UTC), PriceSnapshot: price}); err != nil {
				t.Fatal(err)
			}
			cleanup, err := store.DeleteRows(context.Background(), sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			archive, err := ExportDataBackup(source, full)
			if err != nil {
				t.Fatal(err)
			}
			zr, err := zip.OpenReader(archive)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, entry := range zr.File {
				if entry.Name == "data/"+dataCurrencyRatesFile {
					found++
				}
				if strings.HasPrefix(entry.Name, "data/"+dataCurrencyRatesFile+"-") {
					t.Fatalf("live SQLite sidecar entered archive: %s", entry.Name)
				}
			}
			if err := zr.Close(); err != nil {
				t.Fatal(err)
			}
			if found != 1 {
				t.Fatalf("currency snapshots in archive=%d", found)
			}
			oldRates := createFXBackupFixture(t, target, 9)
			if err := oldRates.Close(); err != nil {
				t.Fatal(err)
			}
			// Sidecars left by an earlier application instance must be rescued,
			// rather than replayed over the newly imported self-contained DB.
			for _, suffix := range []string{"-wal", "-shm"} {
				if err := os.WriteFile(filepath.Join(target, dataCurrencyRatesFile+suffix), []byte("old sidecar"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			gotFull, err := StageDataImport(target, archive)
			if err != nil || gotFull != full {
				t.Fatalf("stage full=%t err=%v", gotFull, err)
			}
			if err := ApplyPendingDataImport(target); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"-wal", "-shm"} {
				if _, err := os.Stat(filepath.Join(target, dataCurrencyRatesFile+suffix)); !os.IsNotExist(err) {
					t.Fatalf("stale sidecar retained after import: %s %v", suffix, err)
				}
				rescues, err := filepath.Glob(filepath.Join(target, "backups", "pre-import-*", dataCurrencyRatesFile+suffix))
				if err != nil || len(rescues) != 1 {
					t.Fatalf("sidecar rescue %s=%v err=%v", suffix, rescues, err)
				}
			}
			restored, err := session.OpenStore(target)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if conversations, err := restored.List(0); err != nil || len(conversations) != 0 {
				t.Fatalf("deleted transcript reappeared: %+v %v", conversations, err)
			}
			var billing []session.UsageRecord
			if err := restored.VisitBilling(context.Background(), "", time.Time{}, func(u session.UsageRecord) { billing = append(billing, u) }); err != nil {
				t.Fatal(err)
			}
			if len(billing) != 1 || billing[0].SessionID != sess.ID || !reflect.DeepEqual(billing[0].PriceSnapshot, price) ||
				billing[0].ContextTool != 200 || !billing[0].ContextEstimateKnown ||
				!billing[0].HasTiming || billing[0].DurationMS != 6000 || billing[0].TTFTMS != 2000 {
				t.Fatalf("restored billing=%+v", billing)
			}
			cache, err := fx.NewWithClient(target, &http.Client{Transport: rejectFXBackupHTTP{t}})
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			rate, ok := cache.Lookup(billing[0].PriceSnapshot.UsageDay, "PLN")
			if !ok || rate.Multiplier != 4 || rate.Date != "2020-02-03" || rate.Source != "NBP table A" || *billing[0].PriceSnapshot.AmountUSD*rate.Multiplier != 20 {
				t.Fatalf("restored historical conversion=%+v ok=%t", rate, ok)
			}
			weekly, ok := cache.Lookup("2020-02-03", "AED")
			if !ok || weekly.Multiplier != 2 || weekly.Date != "2020-01-29" || weekly.USDDate != "2020-02-03" || weekly.Source != "NBP table B / USD table A" {
				t.Fatalf("restored additional quote=%+v %t", weekly, ok)
			}
		})
	}
}

func fxSnapshotArchive(t *testing.T, filename string) string {
	t.Helper()
	root := portableFXBackupDir(t)
	stage := filepath.Join(root, "stage")
	if err := copyIfExists(filename, filepath.Join(stage, "data", dataCurrencyRatesFile)); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(dataBackupMeta{Format: dataBackupFormat, App: "SuperCli"})
	if err := os.WriteFile(filepath.Join(stage, dataBackupManifest), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "backup.zip")
	out, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeZip(out, stage); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestFXBackupRejectsMalformedSnapshotsBeforePendingImport(t *testing.T) {
	for _, name := range []string{"invalid_sqlite", "missing_table", "missing_columns", "nonunique_day", "negative_rate", "missing_currency", "future_publication", "wrong_source"} {
		t.Run(name, func(t *testing.T) {
			source, target := portableFXBackupDir(t), portableFXBackupDir(t)
			filename := filepath.Join(source, dataCurrencyRatesFile)
			if name == "invalid_sqlite" {
				if err := os.WriteFile(filename, []byte("not SQLite"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if name == "missing_table" || name == "missing_columns" || name == "nonunique_day" {
				db, err := sql.Open("sqlite", filename)
				if err != nil {
					t.Fatal(err)
				}
				query := "CREATE TABLE other(value TEXT)"
				if name == "missing_columns" {
					query = "CREATE TABLE currency_days(id INTEGER PRIMARY KEY,usage_day TEXT UNIQUE)"
				}
				if name == "nonunique_day" {
					query = "CREATE TABLE currency_days(id INTEGER PRIMARY KEY,usage_day TEXT,publication_day TEXT,source TEXT,multipliers TEXT)"
				}
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				db := createFXBackupFixture(t, source, 4)
				query := "UPDATE currency_days SET source='unrecognized source'"
				var args []any
				switch name {
				case "negative_rate", "missing_currency":
					values := syntheticFXBackupMultipliers(4)
					if name == "negative_rate" {
						values["PLN"] = -1
					} else {
						delete(values, "GBP")
					}
					raw, err := json.Marshal(values)
					if err != nil {
						t.Fatal(err)
					}
					query, args = "UPDATE currency_days SET multipliers=?", []any{string(raw)}
				case "future_publication":
					query = "UPDATE currency_days SET publication_day='2020-02-04'"
				}
				if _, err := db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := StageDataImport(target, fxSnapshotArchive(t, filename)); err == nil || !strings.Contains(err.Error(), "currency rates") {
				t.Fatalf("malformed rate snapshot accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(target, pendingImportFile)); !os.IsNotExist(err) {
				t.Fatalf("rejected snapshot became pending: %v", err)
			}
		})
	}
}

func TestFXBackupRevalidatesTamperedPendingSnapshotBeforeMovingLiveData(t *testing.T) {
	source, target := portableFXBackupDir(t), portableFXBackupDir(t)
	db := createFXBackupFixture(t, source, 4)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := ExportDataBackup(source, false)
	if err != nil {
		t.Fatal(err)
	}
	oldDB := createFXBackupFixture(t, target, 9)
	if err := oldDB.Close(); err != nil {
		t.Fatal(err)
	}
	liveRates, err := os.ReadFile(filepath.Join(target, dataCurrencyRatesFile))
	if err != nil {
		t.Fatal(err)
	}
	liveProjects := []byte(`{"live":true}`)
	if err := os.WriteFile(filepath.Join(target, "projects.json"), liveProjects, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDataImport(target, archive); err != nil {
		t.Fatal(err)
	}
	pending, err := readPendingDataImport(filepath.Join(target, pendingImportFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending.Stage, "data", dataCurrencyRatesFile), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingDataImport(target); err == nil || !strings.Contains(err.Error(), "currency rates") {
		t.Fatalf("tampered rate snapshot accepted: %v", err)
	}
	for name, want := range map[string][]byte{dataCurrencyRatesFile: liveRates, "projects.json": liveProjects} {
		if got, err := os.ReadFile(filepath.Join(target, name)); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("validation moved live %s: %v", name, err)
		}
	}
	if rescues, err := filepath.Glob(filepath.Join(target, "backups", "pre-import-*")); err != nil || len(rescues) != 0 {
		t.Fatalf("validation began swapping live files: %v %v", rescues, err)
	}
}

func TestFXBackupAllowlistExcludesSidecarsAndNestedFiles(t *testing.T) {
	for _, full := range []bool{false, true} {
		if !allowedBackupPathMode("data/"+dataCurrencyRatesFile, full) || !allowedImportedRoot(dataCurrencyRatesFile, full) {
			t.Fatal("standalone FX snapshot rejected")
		}
		for _, name := range []string{"data/" + dataCurrencyRatesFile + "-wal", "data/" + dataCurrencyRatesFile + "-shm", "data/" + dataCurrencyRatesFile + "/extra", "data/projects/p/" + dataCurrencyRatesFile} {
			if allowedBackupPathMode(name, full) {
				t.Fatalf("unsafe FX snapshot path accepted: %s", name)
			}
		}
	}
}

func TestFXBackupRejectsInvalidAdditionalQuotesBeforeImport(t *testing.T) {
	for name, query := range map[string]string{
		"future":       "UPDATE currency_quotes SET publication_day='2020-02-04'",
		"usd_mismatch": "UPDATE currency_quotes SET usd_publication_day='2020-02-02'",
		"negative":     "UPDATE currency_quotes SET multiplier=-2",
		"orphan":       "UPDATE currency_quotes SET usage_day='2020-02-04'",
		"source":       "UPDATE currency_quotes SET source='invented'",
		"nonunique":    "DROP TABLE currency_quotes; CREATE TABLE currency_quotes(id INTEGER PRIMARY KEY,usage_day TEXT,currency TEXT,multiplier REAL,publication_day TEXT,usd_publication_day TEXT,source TEXT)",
	} {
		t.Run(name, func(t *testing.T) {
			source, target := portableFXBackupDir(t), portableFXBackupDir(t)
			db := createFXBackupFixture(t, source, 4)
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := StageDataImport(target, fxSnapshotArchive(t, filepath.Join(source, dataCurrencyRatesFile))); err == nil {
				t.Fatal("invalid additional quote accepted")
			}
			if _, err := os.Stat(filepath.Join(target, pendingImportFile)); !os.IsNotExist(err) {
				t.Fatalf("invalid quote became pending: %v", err)
			}
		})
	}
}

func TestFXBackupAcceptsOriginalTenCurrencySnapshotWithoutQuoteTable(t *testing.T) {
	source, target := portableFXBackupDir(t), portableFXBackupDir(t)
	db := createFXBackupFixture(t, source, 4)
	if _, err := db.Exec("DROP TABLE currency_quotes"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := ExportDataBackup(source, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StageDataImport(target, archive); err != nil {
		t.Fatalf("old immutable snapshot rejected: %v", err)
	}
	if err := ApplyPendingDataImport(target); err != nil {
		t.Fatal(err)
	}
	c, err := fx.NewWithClient(target, &http.Client{Transport: rejectFXBackupHTTP{t}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if rate, ok := c.Lookup("2020-02-03", "PLN"); !ok || rate.Multiplier != 4 {
		t.Fatalf("old rate lost: %+v %v", rate, ok)
	}
	if _, ok := c.Lookup("2020-02-03", "AED"); ok {
		t.Fatal("missing historic currency invented")
	}
}
