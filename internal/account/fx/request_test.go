package fx

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHistoricalRequestIdentifiesApplication(t *testing.T) {
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "SuperCli/") || r.Header.Get("Accept") != "application/json" {
			http.Error(w, "unidentified API client", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
	})
	cache := testCache(t, portableTestDir(t), client)
	if err := cache.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	requireRate(t, cache, "2026-01-04", "PLN", "2026-01-02", 4)
}

// Opt-in smoke check uses the real official API and an isolated portable cache.
func TestHistoricalRatesLive(t *testing.T) {
	day := os.Getenv("SUPERCLI_TEST_FX_DAY")
	if day == "" {
		t.Skip("set SUPERCLI_TEST_FX_DAY to run the official API smoke check")
	}
	if _, err := parseDay(day); err != nil {
		t.Fatal(err)
	}
	cache := testCache(t, portableTestDir(t), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := cache.EnsureDays(ctx, []string{day}); err != nil {
		t.Fatal(err)
	}
	rate, ok := cache.Lookup(day, "PLN")
	if !ok || !validMultiplier(rate.Multiplier) || rate.Date > day || rate.Source != rateSource {
		t.Fatalf("invalid published rate: %+v, found=%v", rate, ok)
	}
	t.Logf("usage day=%s publication=%s source=%s USD/PLN=%g", day, rate.Date, rate.Source, rate.Multiplier)
}
