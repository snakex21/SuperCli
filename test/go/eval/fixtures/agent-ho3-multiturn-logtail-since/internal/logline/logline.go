// Package logline parses the service log format:
//
//	2026-03-01T10:15:00Z WARN cache miss ratio above 0.4
//
// (RFC 3339 timestamp, level, message).
package logline

import (
	"fmt"
	"strings"
	"time"
)

// Level orders log severities.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

var levelNames = map[string]Level{"DEBUG": Debug, "INFO": Info, "WARN": Warn, "ERROR": Error}

// ParseLevel accepts a level name in any case.
func ParseLevel(s string) (Level, error) {
	l, ok := levelNames[strings.ToUpper(strings.TrimSpace(s))]
	if !ok {
		return 0, fmt.Errorf("unknown level %q", s)
	}
	return l, nil
}

// Entry is one parsed log line; Raw is the line as read.
type Entry struct {
	Time  time.Time
	Level Level
	Msg   string
	Raw   string
}

// Parse parses one line.
func Parse(line string) (Entry, error) {
	fields := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 3)
	if len(fields) < 2 {
		return Entry{}, fmt.Errorf("malformed line %q", line)
	}
	ts, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return Entry{}, fmt.Errorf("bad timestamp: %w", err)
	}
	lvl, err := ParseLevel(fields[1])
	if err != nil {
		return Entry{}, err
	}
	e := Entry{Time: ts, Level: lvl, Raw: strings.TrimRight(line, "\r\n")}
	if len(fields) == 3 {
		e.Msg = fields[2]
	}
	return e, nil
}
