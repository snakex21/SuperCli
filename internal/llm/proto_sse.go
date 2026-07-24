package llm

import (
	"bufio"
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
	scanner := bufio.NewScanner(r)
	// Some SSE payloads (especially the [DONE] terminator plus one
	// large final chunk) exceed the default 64 KiB token size.
	// 1 MiB is plenty for chat-completion chunks.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		event string
		data  strings.Builder
	)
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		err := onEvent(event, data.String())
		event = ""
		data.Reset()
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
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
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		case "id", "retry":
			// last-event-id and retry intervals are not relevant
			// for the OpenAI chat-completions stream; ignore.
		default:
			// unknown field, ignore
		}
	}
	if err := scanner.Err(); err != nil {
		return err
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
