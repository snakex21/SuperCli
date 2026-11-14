// Package auth issues sessions.
package auth

import (
	"time"

	"gateway/internal/util"
)

// Session is a login session.
type Session struct {
	ID      string
	User    string
	Created time.Time
}

// Start creates a session.
func Start(user string, now time.Time) Session {
	return Session{ID: util.NewID("sess"), User: util.Slugify(user), Created: now}
}

// Age describes how old s is.
func Age(s Session, now time.Time) string {
	return util.FormatAgo(now.Sub(s.Created))
}
