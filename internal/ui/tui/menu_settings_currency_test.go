package tui

import (
	"fmt"
	"strings"
	"testing"

	"supercli/internal/account/fx"
	"supercli/internal/system/config"
)

func TestCostCurrencySettingsRowDefaultAndReset(t *testing.T) {
	var row settingRow
	for _, candidate := range settingsRowsFor("pl") {
		if candidate.key == "cost_currency" {
			row = candidate
		}
	}
	if row.key == "" || row.kind != setText || row.nextSession {
		t.Fatalf("missing live currency editor: %+v", row)
	}
	if !strings.Contains(row.desc, fmt.Sprint(len(fx.SupportedCurrencies()))) {
		t.Fatalf("currency editor does not advertise shared catalog size: %+v", row)
	}
	c := config.TomlConfig{}
	if value, source := (Model{}).settingValueSource(row, &c); value != "USD" || source != "default" {
		t.Fatalf("default=%q source=%q", value, source)
	}
	c.CostCurrency = "PLN"
	if value := settingTextValue(&c, "cost_currency"); value != "PLN" {
		t.Fatalf("editable currency=%q", value)
	}
	settingResetKey(&c, "cost_currency")
	if c.CostCurrency != "" || settingTextValue(&c, "cost_currency") != "USD" {
		t.Fatal("reset did not restore USD")
	}
	for _, code := range []string{"AUD", "AED", "CNY", "INR", "VND"} {
		c.CostCurrency = code
		if value := settingTextValue(&c, "cost_currency"); value != code {
			t.Fatalf("new currency %s editor value=%s", code, value)
		}
	}
}
