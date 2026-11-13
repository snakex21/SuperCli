package billing

// Invoice is a customer invoice.
type Invoice struct {
	ID       string
	TotalCts int
	Overdue  bool
}
