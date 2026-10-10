package config

import "supercli/internal/account/fx"

// EffectiveCostCurrency keeps legacy or invalid hand-edited preferences safe.
// UI setters validate explicitly; stored costs and provider prices stay in USD.
func EffectiveCostCurrency(tc TomlConfig) string {
	code, err := fx.NormalizeCurrency(tc.CostCurrency)
	if err != nil {
		return "USD"
	}
	return code
}
