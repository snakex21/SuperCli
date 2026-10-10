package usagecost

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHistoryRatesSelectedCurrencyWarmsMissingQuoteOnCachedDay(t *testing.T) {
	var calls atomic.Int32
	h := NewHistoryRatesWithClient(portableHistoryRatesDir(t), &http.Client{Transport: historyRatesTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if strings.Contains(req.URL.Path, "/tables/a/") {
			return historyRatesResponse(req, "2026-01-09"), nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: req, Body: io.NopCloser(strings.NewReader(`[{"table":"B","effectiveDate":"2026-01-07","rates":[{"code":"AED","mid":2},{"code":"VND","mid":0.001}]}]`))}, nil
	})})
	defer h.Close()
	if err := h.Ensure(context.Background(), []string{"2026-01-09"}); err != nil {
		t.Fatal(err)
	}
	h.WarmCurrency("AED", "2026-01-09")
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := h.Cache()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := c.Lookup("2026-01-09", "AED")
	if !ok || r.Multiplier != 2 || r.Date != "2026-01-07" || r.USDDate != "2026-01-09" || calls.Load() != 2 {
		t.Fatalf("selected quote=%+v %t calls=%d", r, ok, calls.Load())
	}
	h.WarmCurrency("VND", "2026-01-09")
	h.WarmCurrency("USD", "2026-01-09")
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending, generation := h.State(); pending || generation != 1 || calls.Load() != 2 {
		t.Fatalf("cached sibling or USD started work: %v %d calls=%d", pending, generation, calls.Load())
	}
}
