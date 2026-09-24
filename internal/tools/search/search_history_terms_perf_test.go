package search

import "testing"

var historyQueryBenchResult string

func BenchmarkHistoryQueryTerms(b *testing.B) {
	for _, query := range []string{"RetryDelay MaxAttempts", "BudgetLimit src/retry-policy.go", "NEAR(\"exact phrase\" prefix*, 3) OR content:^alpha"} {
		b.Run(query, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				historyQueryBenchResult = historyQueryTerms(query)
			}
		})
	}
}
