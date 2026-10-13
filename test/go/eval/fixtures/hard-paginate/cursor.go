package pager

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// ErrBadCursor is returned (possibly wrapped) for a malformed cursor.
var ErrBadCursor = errors.New("pager: invalid cursor")

const cursorPrefix = "after:"

// encodeCursor builds an opaque cursor pointing just after lastID.
func encodeCursor(lastID int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorPrefix + strconv.Itoa(lastID)))
}

// decodeCursor returns the ID stored in a cursor made by encodeCursor.
func decodeCursor(c string) (int, error) {
	raw, err := base64.URLEncoding.DecodeString(c)
	if err != nil {
		return 0, ErrBadCursor
	}
	s := string(raw)
	if !strings.HasPrefix(s, cursorPrefix) {
		return 0, ErrBadCursor
	}
	id, err := strconv.Atoi(strings.TrimPrefix(s, cursorPrefix))
	if err != nil {
		return 0, ErrBadCursor
	}
	return id, nil
}
