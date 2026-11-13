package billing

// LateFeePercent is applied to overdue invoices.
const LateFeePercent = 5

// LateFee returns the late fee in cents for an overdue amount.
func LateFee(amountCts int) int { return amountCts * LateFeePercent / 100 }
