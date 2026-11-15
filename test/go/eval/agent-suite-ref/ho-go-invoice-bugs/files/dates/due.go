// Package dates computes payment due dates.
package dates

import "time"

// DueDate returns issued + netDays. A due date on a Saturday or Sunday
// moves to the following Monday.
func DueDate(issued time.Time, netDays int) time.Time {
	due := issued.AddDate(0, 0, netDays)
	switch due.Weekday() {
	case time.Saturday:
		due = due.AddDate(0, 0, 2)
	case time.Sunday:
		due = due.AddDate(0, 0, 1)
	}
	return due
}
