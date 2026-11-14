// Package admin implements the admin console backend.
package admin

import (
	"fmt"
	"time"

	"gateway/internal/util"
)

// Event is an audit event.
type Event struct {
	ID   string
	What string
	At   time.Time
}

// Describe renders an event line.
func Describe(e Event, now time.Time, width int) string {
	return util.Truncate(fmt.Sprintf("%s %s (%s)", e.ID, e.What, util.FormatAgo(now.Sub(e.At))), width)
}

// Record creates an event.
func Record(what string, at time.Time) Event {
	return Event{ID: util.NewID("evt"), What: what, At: at}
}
