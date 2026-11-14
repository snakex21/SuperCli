// Package auth issues sessions.
package auth

import (
	"time"

	"gateway/internal/clock"
	"gateway/internal/ids"
	"gateway/internal/textutil"
)

// Session is a login session.
type Session struct {
	ID      string
	User    string
	Created time.Time
}

// Start creates a session.
func Start(user string, now time.Time) Session {
	return Session{ID: ids.New("sess"), User: textutil.Slugify(user), Created: now}
}

// Age describes how old s is.
func Age(s Session, now time.Time) string {
	return clock.FormatAgo(now.Sub(s.Created))
}
