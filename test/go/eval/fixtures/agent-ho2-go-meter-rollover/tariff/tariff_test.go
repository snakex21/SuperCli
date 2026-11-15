package tariff

import "testing"

func TestPrice(t *testing.T) {
	cases := map[int64]int64{0: 1850, 100: 1850 + 6200, 300: 1850 + 18600, 400: 1850 + 18600 + 7100,
		1200: 1850 + 18600 + 49700 + 17000}
	for kwh, want := range cases {
		if got := Household.Price(kwh); got != want {
			t.Errorf("Price(%d) = %d, want %d", kwh, got, want)
		}
	}
}
