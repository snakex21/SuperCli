package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/account/usagecost"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/stats"
)

func portableUsageCurrencyDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".tmp", "tui-currency-tests"))
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

type rejectUsageCurrencyHTTP struct{ t *testing.T }

func (r rejectUsageCurrencyHTTP) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("opening statistics initiated FX HTTP: %s", req.URL)
	return nil, fmt.Errorf("statistics are read-only")
}

func savedUsageCurrencyDay(t *testing.T, root, day string, pln float64) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "currency-rates.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS currency_days (
		id INTEGER PRIMARY KEY AUTOINCREMENT, usage_day TEXT NOT NULL UNIQUE,
		publication_day TEXT NOT NULL, source TEXT NOT NULL, multipliers TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	multipliers := make(map[string]float64)
	for _, code := range fx.SupportedCurrencies() {
		multipliers[code] = 2
	}
	multipliers["USD"], multipliers["PLN"] = 1, pln
	raw, err := json.Marshal(multipliers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO currency_days(usage_day,publication_day,source,multipliers) VALUES(?,?,'NBP table A',?)`, day, day, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func loadUsageCurrencySnapshot(t *testing.T, m Model) *usageSnapshot {
	t.Helper()
	msg := m.loadUsage()().(usageLoadedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.data == nil {
		t.Fatal("usage snapshot missing")
	}
	return msg.data
}

func TestUsageCurrencyMenuUsesPersistedPricesAndCurrentProcessScope(t *testing.T) {
	root := portableUsageCurrencyDir(t)
	tc := config.TomlConfig{CostCurrency: "PLN", Providers: []config.ProviderConf{
		{Name: "cloud", Type: config.ProviderOpenAI, Model: "m", BaseURL: "https://api.openai.com/v1", APIKey: "test-cloud"},
		{Name: "helper", Type: config.ProviderOpenAI, Model: "h", BaseURL: "https://helper.example/v1", APIKey: "test-helper"},
	}, ModelPrices: []config.ModelPriceConf{{Provider: "cloud", Model: "m", InputCost: 900}, {Provider: "helper", Model: "h", InputCost: 700}}}
	global, _ := config.FindTomlPaths(root, root)
	if err := config.SaveToml(global, tc); err != nil {
		t.Fatal(err)
	}
	savedUsageCurrencyDay(t, root, "2026-10-07", 4)
	savedUsageCurrencyDay(t, root, "2026-10-08", 5)
	store, err := session.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	active, err := store.Create(root, "m", "active")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(root, "h", "earlier conversation in this process")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	appendFrozen := func(id, provider, model, endpoint, purpose string, at time.Time, amount float64, reasoning int64) {
		price := &session.PriceSnapshot{State: "manual", AmountUSD: &amount, Source: "manual", InputPerMillion: 1,
			CacheKnown: true, UsageDay: at.In(time.Local).Format("2006-01-02"), PriceDate: "2026-10-07"}
		if err := store.AppendUsage(context.Background(), session.UsageRecord{SessionID: id, Provider: provider, ProviderType: "openai",
			EndpointHost: endpoint, Model: model, Source: purpose, CreatedAt: at, Input: int64(amount * 1000000), Output: reasoning,
			Reasoning: reasoning, HasReasoning: reasoning > 0, PriceSnapshot: price}); err != nil {
			t.Fatal(err)
		}
	}
	appendFrozen(active.ID, "cloud", "m", "api.openai.com", "main", started.Add(-time.Hour), 99, 0)
	appendFrozen(active.ID, "cloud", "m", "api.openai.com", "main", started, 1, 70)
	appendFrozen(other.ID, "helper", "h", "helper.example", "compact", started.AddDate(0, 0, 1), 2, 20)
	if err := store.Delete(other.ID); err != nil {
		t.Fatal(err)
	}
	rec := stats.NewMemory()
	rec.RecordCall(stats.Call{Purpose: "main", Model: "m", Provider: "openai", ProviderType: "openai", EndpointHost: "api.openai.com",
		ConnectionKey: llm.ProviderConnectionKey("openai", tc.Providers[0].BaseURL, tc.Providers[0].APIKey), StartedAt: started,
		TokensIn: 1000000, TokensOut: 70, TokensReasoning: 70})
	rec.RecordCall(stats.Call{Purpose: "compact", Model: "h", Provider: "openai", ProviderType: "openai", EndpointHost: "helper.example",
		ConnectionKey: llm.ProviderConnectionKey("openai", tc.Providers[1].BaseURL, tc.Providers[1].APIKey), StartedAt: started.AddDate(0, 0, 1),
		TokensIn: 2000000, TokensOut: 20, TokensReasoning: 20})
	rates := usagecost.NewHistoryRatesWithClient(root, &http.Client{Transport: rejectUsageCurrencyHTTP{t}})
	defer rates.Close()
	m := New(Options{Home: root, DataDir: root, SessionID: active.ID, SessionStore: store, StatsRecorder: rec, UsageRates: rates, ActiveProvider: "cloud", Language: "en", NoColor: true})
	m.loadedSessionID = active.ID
	for i := 0; i < 2; i++ {
		d := loadUsageCurrencySnapshot(t, m)
		if d.input != 3000000 || d.output != 90 || d.reasoning != 90 || d.cost.Amount == nil || *d.cost.Amount != 14 || d.cost.BaseAmountUSD == nil || *d.cost.BaseAmountUSD != 3 || d.cost.Currency != "PLN" || d.cost.MissingFXCalls != 0 {
			t.Fatalf("current-process scope or frozen prices changed: %+v cost=%+v", d, d.cost)
		}
		if label := m.costLabel(d.cost); !strings.Contains(label, "14.000000 PLN") || strings.Contains(label, "$") {
			t.Fatalf("selected currency label=%q", label)
		}
	}
	m.menu.category = 1
	d := loadUsageCurrencySnapshot(t, m)
	if d.input != 100000000 || d.cost.Amount == nil || *d.cost.Amount != 400 || d.cost.BaseAmountUSD == nil || *d.cost.BaseAmountUSD != 100 || d.cost.Currency != "PLN" {
		t.Fatalf("selected-session scope or saved USD prices changed: %+v cost=%+v", d, d.cost)
	}
}

func TestUsageCurrencyMenuMissingRatesAndActualHelperFallbackStayOffline(t *testing.T) {
	root := portableUsageCurrencyDir(t)
	tc := config.TomlConfig{CostCurrency: "PLN", Providers: []config.ProviderConf{
		{Name: "cloud", Type: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "main"},
		{Name: "helper", Type: "openai", BaseURL: "https://helper.example/v1", APIKey: "helper", Model: "same-id"},
	}, ModelPrices: []config.ModelPriceConf{{Provider: "cloud", Model: "same-id", InputCost: 900}, {Provider: "helper", Model: "same-id", InputCost: 2}}}
	global, _ := config.FindTomlPaths(root, root)
	if err := config.SaveToml(global, tc); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 8, 12, 0, 0, 0, time.Local)
	rec := stats.NewMemory()
	rec.RecordCall(stats.Call{Purpose: "compact", Model: "same-id", Provider: "openai", ProviderType: "openai", EndpointHost: "helper.example",
		ConnectionKey: llm.ProviderConnectionKey("openai", tc.Providers[1].BaseURL, tc.Providers[1].APIKey), StartedAt: started, TokensIn: 1000000})
	rates := usagecost.NewHistoryRatesWithClient(root, &http.Client{Transport: rejectUsageCurrencyHTTP{t}})
	defer rates.Close()
	m := New(Options{Home: root, DataDir: root, StatsRecorder: rec, UsageRates: rates, ActiveProvider: "cloud", NoColor: true, Language: "en"})
	d := loadUsageCurrencySnapshot(t, m)
	if d.cost.Amount != nil || d.cost.BaseAmountUSD == nil || *d.cost.BaseAmountUSD != 2 || d.cost.MissingFXCalls != 1 || !d.cost.Partial || d.cost.Source != "fx_missing" {
		t.Fatalf("unknown rate or helper identity misreported: %+v", d.cost)
	}
	if label := m.costLabel(d.cost); !strings.Contains(label, "PLN") || strings.Contains(label, "0.000000") || strings.Contains(label, "$") {
		t.Fatalf("missing-rate label=%q", label)
	}
	// Simulate another application instance saving the day. Refresh on the
	// next local menu read must import it without any fetch/Ensure/Warm.
	savedUsageCurrencyDay(t, root, "2026-10-08", 4)
	d = loadUsageCurrencySnapshot(t, m)
	if d.cost.Amount == nil || *d.cost.Amount != 8 || d.cost.MissingFXCalls != 0 || d.cost.Partial {
		t.Fatalf("local Refresh did not observe saved rate: %+v", d.cost)
	}
	if pending, generation := rates.State(); pending || generation != 0 {
		t.Fatalf("menu opening started background exchange work: pending=%t generation=%d", pending, generation)
	}
}

func TestUsageCostCurrencyLabelsKeepUSDAndIncludedClassifications(t *testing.T) {
	m := New(Options{Language: "en", NoColor: true})
	amount := .125
	for _, currency := range []string{"USD", "PLN", "JPY", "AUD", "AED", "CNY", "INR", "VND"} {
		label := m.costLabel(usagecost.Summary{State: "estimated", Currency: currency, Amount: &amount, Estimated: true, Partial: true})
		if !strings.Contains(label, "~0.125000 "+currency) {
			t.Fatalf("currency=%s label=%q", currency, label)
		}
	}
	for _, state := range []string{"free", "local", "subscription"} {
		if label := m.costLabel(usagecost.Summary{State: state, Currency: "PLN"}); label == "" || strings.Contains(label, "0.000000") {
			t.Fatalf("classification=%s label=%q", state, label)
		}
	}
}
