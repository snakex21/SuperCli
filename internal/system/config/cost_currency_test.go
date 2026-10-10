package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestCostCurrencyDefaultsNormalizeAndMerge(t *testing.T) {
	for raw, want := range map[string]string{"": "USD", " pln ": "PLN", "EUR": "EUR", " aud ": "AUD", "AED": "AED", "CNY": "CNY", "INR": "INR", "KRW": "KRW", "VND": "VND", "XDR": "USD", "invalid": "USD"} {
		if got := EffectiveCostCurrency(TomlConfig{CostCurrency: raw}); got != want {
			t.Fatalf("%q effective=%q want=%q", raw, got, want)
		}
	}
	c := TomlConfig{CostCurrency: "PLN"}
	mergeToml(&c, TomlConfig{})
	if c.CostCurrency != "PLN" {
		t.Fatal("absent layer overwrote currency")
	}
	mergeToml(&c, TomlConfig{CostCurrency: "USD"})
	if c.CostCurrency != "USD" {
		t.Fatal("explicit USD override lost")
	}
	var decoded TomlConfig
	if _, err := toml.Decode("cost_currency = \"CHF\"\n", &decoded); err != nil || decoded.CostCurrency != "CHF" {
		t.Fatalf("TOML currency: %+v %v", decoded, err)
	}
}
