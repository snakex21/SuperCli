package llm

import (
	"errors"
	"io"
	"strings"
)

// parseSSE reads a Server-Sent Events stream from r and invokes
// onEvent for every complete event. An "event" is a non-empty data
// payload (possibly multi-line, joined with \n) terminated by a
// blank line. Comments (lines starting with ':') and unknown
// fields are ignored. The event name is reported as the first
// argument; the data lines as the second.
//
// Returns the first error from onEvent, the scanner, or io.EOF.
// The function reads until EOF or error; callers expecting a
// single stream should wrap onEvent to short-circuit on a "done"
// marker.
func parseSSE(r io.Reader, onEvent func(eventName, data string) error) error {
	// Lines of any length up to maxSSELineBytes (a 1 MiB bufio.Scanner
	// failed large single-line tool calls with "token too long").
	lines := newSSELineReader(r)

	var (
		event string
		data  string
		multi strings.Builder
	)
	flush := func() error {
		if data == "" && multi.Len() == 0 {
			return nil
		}
		payload := data
		if multi.Len() > 0 {
			payload = multi.String()
		}
		err := onEvent(event, payload)
		event = ""
		data = ""
		multi.Reset()
		return err
	}
	for {
		line, err := lines.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			// comment / heartbeat, ignore
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			// field with no value; treat as field with empty value
			continue
		}
		field := line[:colon]
		value := line[colon+1:]
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			event = value
		case "data":
			// scanner.Text owns its string. Keep the usual single-line payload
			// without a second copy; only multi-line data needs assembly.
			switch {
			case multi.Len() > 0:
				multi.WriteByte('\n')
				multi.WriteString(value)
			case data != "":
				multi.Grow(len(data) + 1 + len(value))
				multi.WriteString(data)
				multi.WriteByte('\n')
				multi.WriteString(value)
				data = ""
			default:
				data = value
			}
		case "id", "retry":
			// last-event-id and retry intervals are not relevant
			// for the OpenAI chat-completions stream; ignore.
		default:
			// unknown field, ignore
		}
	}
	// Trailing event without a blank line terminator.
	if err := flush(); err != nil {
		return err
	}
	return nil
}

// sseDoneSentinel is the literal "data: [DONE]" terminator used by
// OpenAI and most OpenAI-compat providers (Azure, Together, Groq,
// OpenRouter, etc.).
const sseDoneSentinel = "[DONE]"

// isDone reports whether an SSE data payload is the [DONE] sentinel.
func isDone(data string) bool { return strings.TrimSpace(data) == sseDoneSentinel }

// isSSEHeartbeatData recognizes the small non-JSON data frames used by some
// gateways to keep an otherwise idle streaming connection alive. Arbitrary
// malformed payloads are errors; only these explicit heartbeat spellings are
// safe to ignore.
func isSSEHeartbeatData(data string) bool {
	switch strings.ToLower(strings.TrimSpace(data)) {
	case "ping", "pong", "keepalive", "keep-alive", "[keepalive]", "[keep-alive]":
		return true
	default:
		return false
	}
}

// errSSEUnexpected is returned by OpenAI's parser when the server
// sent a payload that is neither JSON nor the [DONE] sentinel.
var errSSEUnexpected = errors.New("llm: unexpected SSE payload")
