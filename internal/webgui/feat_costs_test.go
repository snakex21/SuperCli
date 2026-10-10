package webgui

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/account/usagecost"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type costsTransport func(*http.Request) (*http.Response, error)

func (f costsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixedCostRatesClient(t *testing.T, requests *atomic.Int32) *http.Client {
	t.Helper()
	var tables []map[string]any
	for i, day := range []string{"2026-10-07", "2026-10-08"} {
		var rates []map[string]any
		for _, code := range fx.SupportedCurrencies() {
			if code == "PLN" {
				continue
			}
			mid := 2.0
			if code == "USD" {
				mid = float64(4 + i)
			}
			rates = append(rates, map[string]any{"code": code, "mid": mid})
		}
		tables = append(tables, map[string]any{"table": "A", "effectiveDate": day, "rates": rates})
	}
	data, _ := json.Marshal(tables)
	return &http.Client{Transport: costsTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.URL.Scheme != "https" || r.URL.Host != "api.nbp.pl" {
			t.Errorf("unexpected URL=%s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header), Request: r}, nil
	})}
}

func TestCostsUsesSavedDailyRatesAndRetainsDeletedConversation(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "m", "billing")
	if err != nil {
		t.Fatal(err)
	}
	tc := config.TomlConfig{CostCurrency: "PLN", ModelPrices: []config.ModelPriceConf{{Provider: "a", Model: "m", InputCost: 1}, {Provider: "b", Model: "n", InputCost: 2}}}
	global, _ := config.FindTomlPaths(s.eng.DataDir(), s.eng.Home())
	if err := config.SaveToml(global, tc); err != nil {
		t.Fatal(err)
	}
	for i, provider := range []string{"a", "b"} {
		u := session.UsageRecord{SessionID: sess.ID, Provider: provider, Model: []string{"m", "n"}[i], Input: 1_000_000, Source: "main", CreatedAt: time.Date(2026, 10, 7+i, 12, 0, 0, 0, time.Local)}
		price := usagecost.FreezeQuote(tc, u, false)
		u.PriceSnapshot = &price
		if err := store.AppendUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	s.eng.costRates = usagecost.NewHistoryRatesWithClient(s.eng.DataDir(), fixedCostRatesClient(t, &requests))
	if err := s.eng.ensureCostRates(context.Background(), sess.ID); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("historical requests=%d", requests.Load())
	}
	tc.ModelPrices[0].InputCost = 900
	tc.ModelPrices[1].InputCost = 700
	if err := config.SaveToml(global, tc); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		s.handleCosts(rec, httptest.NewRequest("GET", "/api/costs?session="+sess.ID, nil))
		var got costsView
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || len(got.Rows) != 2 || got.Total.Amount == nil || *got.Total.Amount != 14 || got.Total.BaseAmountUSD == nil || *got.Total.BaseAmountUSD != 3 || got.Currency != "PLN" {
			t.Fatalf("status=%d result=%+v", rec.Code, got)
		}
		if got.Rows[0].Cost.InputPerMillion == nil || *got.Rows[0].Cost.InputPerMillion != 1 || got.Rows[0].Cost.PricingCurrency != "USD" {
			t.Fatalf("USD price changed: %+v", got.Rows[0].Cost)
		}
		main, err := s.eng.stats(context.Background(), sess.ID)
		if err != nil || main.Cost.Amount == nil || *main.Cost.Amount != 14 {
			t.Fatalf("sidebar disagrees: %+v err=%v", main.Cost, err)
		}
		if main.Pricing.Currency != "USD" || main.Pricing.InputPerMillion == nil || *main.Pricing.InputPerMillion != 700 {
			t.Fatalf("editable current quote=%+v", main.Pricing)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("panel reads queried FX API: %d", requests.Load())
	}
	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	all, err := s.eng.costs(context.Background(), "")
	if err != nil || len(all.Rows) != 2 || all.Total.Amount == nil || *all.Total.Amount != 14 {
		t.Fatalf("deleted history lost: %+v err=%v", all, err)
	}
}

func TestConvertedCostMissingRateCannotBecomeZero(t *testing.T) {
	tc := config.TomlConfig{CostCurrency: "PLN", ModelPrices: []config.ModelPriceConf{{Model: "m", InputCost: 1}}}
	cost := newConvertedCost(tc, nil)
	u := session.UsageRecord{Model: "m", Input: 1_000_000, CreatedAt: time.Now()}
	price := usagecost.FreezeQuote(tc, u, false)
	u.PriceSnapshot = &price
	cost.Add(u)
	got := cost.Summary()
	if got.Amount != nil || got.BaseAmountUSD == nil || *got.BaseAmountUSD != 1 || !got.Partial || cost.MissingFXCalls() != 1 || got.Source != "fx_missing" {
		t.Fatalf("missing rate became known: %+v missing=%d", got, cost.MissingFXCalls())
	}
}

func TestUsageSinkRecordsActualWorkerAndStablePrice(t *testing.T) {
	s := newTestServer(t, false)
	store, _ := s.eng.sessionStore()
	sess, _ := store.Create(s.eng.Home(), "main", "identity")
	tc := config.TomlConfig{Providers: []config.ProviderConf{{Name: "main", Type: "openai", BaseURL: "https://main.example", Model: "main"}, {Name: "helper", Type: "openai", BaseURL: "https://other.example", APIKey: "secret", Model: "helper"}}, ModelPrices: []config.ModelPriceConf{{Provider: "helper", Model: "helper", InputCost: 1}}}
	global, _ := config.FindTomlPaths(s.eng.DataDir(), s.eng.Home())
	if err := config.SaveToml(global, tc); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	s.eng.costRates = usagecost.NewHistoryRatesWithClient(s.eng.DataDir(), fixedCostRatesClient(t, &requests))
	sink := s.eng.usageCallSink(store, sess.ID)
	sink(llm.CallStat{Provider: "openai", ProviderType: "openai", EndpointHost: "other.example", ConnectionKey: llm.ProviderConnectionKey("openai", "https://other.example", "secret"), Model: "helper", Purpose: llm.PurposeTask, TokensIn: 1_000_000, StartedAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.Local)})
	rows, err := store.ReadUsage(context.Background(), sess.ID)
	if err != nil || len(rows) != 1 || rows[0].Provider != "helper" || rows[0].EndpointHost != "other.example" || rows[0].PriceSnapshot == nil || math.Abs(*rows[0].PriceSnapshot.AmountUSD-1) > 1e-12 {
		t.Fatalf("usage=%+v err=%v", rows, err)
	}
	if requests.Load() != 0 {
		t.Fatal("USD usage unnecessarily fetched exchange rates")
	}
}

func TestCostRatesReadyWaitsForExistingWarmWithoutStartingOrRetryingFetch(t *testing.T) {
	s := newTestServer(t, false)
	var requests atomic.Int32
	fixture := fixedCostRatesClient(t, &requests)
	started, release := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	client := &http.Client{Transport: costsTransport(func(r *http.Request) (*http.Response, error) {
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
			return fixture.Transport.RoundTrip(r)
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	rates := usagecost.NewHistoryRatesWithClient(s.eng.DataDir(), client)
	s.eng.costRates = rates
	rates.Warm("2026-10-08")
	<-started
	if pending, generation := rates.State(); !pending || generation == 0 {
		t.Fatalf("pending=%v generation=%d", pending, generation)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cancelRec := httptest.NewRecorder()
	s.handleCostRatesReady(cancelRec, httptest.NewRequest("GET", "/api/costs/ready", nil).WithContext(canceled))
	if cancelRec.Code != http.StatusGatewayTimeout || !rates.Pending() {
		t.Fatal("canceling a browser wait affected background rates")
	}
	done := make(chan struct{})
	rec := httptest.NewRecorder()
	go func() {
		defer close(done)
		s.handleCostRatesReady(rec, httptest.NewRequest("GET", "/api/costs/ready", nil))
	}()
	releaseOnce.Do(func() { close(release) })
	<-done
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) || requests.Load() != 1 || rates.Pending() {
		t.Fatalf("status=%d requests=%d pending=%v", rec.Code, requests.Load(), rates.Pending())
	}
	for i := 0; i < 3; i++ {
		idleRec := httptest.NewRecorder()
		s.handleCostRatesReady(idleRec, httptest.NewRequest("GET", "/api/costs/ready", nil))
		if idleRec.Code != http.StatusOK || requests.Load() != 1 {
			t.Fatal("waiting on idle rates initiated another fetch")
		}
	}
}
