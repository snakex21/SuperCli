package webgui

import (
	"testing"

	"supercli/internal/system/config"
)

func TestCurrencyKnobDefaultValidationAndReset(t *testing.T) {
	c := config.TomlConfig{}
	value, source, raw := knobValue(&c, "cost_currency")
	if value != "USD" || raw != "USD" || source != "default" || knobDefault("cost_currency") != "USD" {
		t.Fatalf("default currency value=%q raw=%q source=%q", value, raw, source)
	}
	found := false
	for _, def := range knobDefs() {
		if def.key == "cost_currency" {
			found = true
			if def.kind != knobCurrency || def.nextSession {
				t.Fatalf("currency should be a live picker: %+v", def)
			}
		}
	}
	if !found {
		t.Fatal("missing currency knob")
	}
	if err := knobSet(&c, "cost_currency", " pln "); err != nil || c.CostCurrency != "PLN" {
		t.Fatalf("set normalized currency: %+v %v", c, err)
	}
	if err := knobSet(&c, "cost_currency", "BTC"); err == nil || c.CostCurrency != "PLN" {
		t.Fatal("invalid currency changed existing preference")
	}
	if value, source, raw := knobValue(&c, "cost_currency"); value != "PLN" || raw != "PLN" || source != "manual" {
		t.Fatalf("manual currency value=%q raw=%q source=%q", value, raw, source)
	}
	if err := knobSet(&c, "cost_currency", "default"); err != nil || c.CostCurrency != "" {
		t.Fatalf("default reset: %+v %v", c, err)
	}
}
