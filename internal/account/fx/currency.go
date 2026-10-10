// Package fx stores historical USD conversion rates in the application's
// portable data directory. It never starts a timer or a background refresh.
package fx

import (
	"fmt"
	"strings"
)

type currencyInfo struct {
	code, table string
	legacy      bool
}

// Official NBP A/B currency catalogue checked 2026-10-10. XDR is a reserve
// accounting unit rather than a currency. No crypto, metals or invented pegs.
// The original display order and legacy snapshot requirements stay stable.
var currencyCatalog = func() []currencyInfo {
	list := []currencyInfo{
		{"USD", "A", true}, {"PLN", "", true}, {"EUR", "A", true},
		{"GBP", "A", true}, {"CHF", "A", true}, {"JPY", "A", true},
		{"CAD", "A", true}, {"CZK", "A", true}, {"NOK", "A", true}, {"SEK", "A", true},
	}
	for _, code := range strings.Fields("AUD BRL CLP CNY DKK HKD HUF IDR ILS INR ISK KRW MXN MYR NZD PHP RON SGD THB TRY UAH ZAR") {
		list = append(list, currencyInfo{code: code, table: "A"})
	}
	for _, code := range strings.Fields("AED AFN ALL AMD AOA ARS AWG AZN BAM BBD BDT BHD BIF BND BOB BSD BWP BYN BZD CDF COP CRC CUP CVE DJF DOP DZD EGP ERN ETB FJD GEL GHS GIP GMD GNF GTQ GYD HNL HTG IQD IRR JMD JOD KES KGS KHR KMF KWD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MZN NAD NGN NIO NPR OMR PAB PEN PGK PKR PYG QAR RSD RUB RWF SAR SBD SCR SDG SLE SOS SRD SSP STN SVC SYP SZL TJS TMT TND TOP TTD TWD TZS UGX UYU UZS VES VND VUV WST XAF XCD XCG XOF XPF YER ZMW ZWG") {
		list = append(list, currencyInfo{code: code, table: "B"})
	}
	return list
}()

var supportedCurrencies = func() []string {
	list := make([]string, len(currencyCatalog))
	for i, currency := range currencyCatalog {
		list[i] = currency.code
	}
	return list
}()

func currencyTable(code string) string {
	for _, currency := range currencyCatalog {
		if currency.code == code {
			return currency.table
		}
	}
	return ""
}

// SupportedCurrencies returns an independent list in display order.
func SupportedCurrencies() []string {
	return append([]string(nil), supportedCurrencies...)
}

// NormalizeCurrency accepts ISO currency codes; an empty preference means USD.
func NormalizeCurrency(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "USD", nil
	}
	for _, code := range supportedCurrencies {
		if value == code {
			return code, nil
		}
	}
	return "", fmt.Errorf("unsupported cost currency %q", value)
}
