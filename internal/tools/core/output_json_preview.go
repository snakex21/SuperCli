package core

import (
	"encoding/json"
	"unicode/utf8"
)

// PreviewJSONString bounds an encoded JSON string between complete escape/rune
// units. encoded must be a valid JSON string and budget must be at least 2.
// Encoding first includes control-character and HTML escaping in the byte cap.
func PreviewJSONString(encoded json.RawMessage, budget int) (json.RawMessage, bool) {
	if len(encoded) <= budget {
		return encoded, false
	}
	const omitted = `\n[... output omitted from preview ...]\n`
	keep := budget - len(omitted) - 2
	if keep <= 0 {
		return json.RawMessage(`""`), true
	}
	payload := encoded[1 : len(encoded)-1]
	headLimit := keep * 3 / 4
	tailLimit := len(payload) - (keep - headLimit)
	head := 0
	for i := 0; i < headLimit; {
		next := i + 1
		switch {
		case payload[i] == '\\':
			next = i + 2
			if payload[i+1] == 'u' {
				next = i + 6
			}
		case payload[i] >= utf8.RuneSelf:
			_, size := utf8.DecodeRune(payload[i:])
			next = i + size
		}
		if next > headLimit {
			break
		}
		head = next
		i = next
	}
	tail := previewJSONStringTailBoundary(payload, tailLimit)
	result := make([]byte, 0, budget)
	result = append(result, '"')
	result = append(result, payload[:head]...)
	result = append(result, omitted...)
	result = append(result, payload[tail:]...)
	result = append(result, '"')
	return result, true
}

// previewJSONStringTailBoundary finds the same first unit end at or beyond at
// without scanning the omitted middle. Only a preceding backslash run can need
// a longer scan: its parity determines whether the nearby slash is escaped.
func previewJSONStringTailBoundary(payload []byte, at int) int {
	if at >= len(payload) {
		return len(payload)
	}
	if !utf8.RuneStart(payload[at]) {
		for at < len(payload) && !utf8.RuneStart(payload[at]) {
			at++
		}
		return at
	}
	for i := at - 1; i >= 0 && i >= at-5; i-- {
		if payload[i] != '\\' {
			continue
		}
		slashes := 1
		for j := i - 1; j >= 0 && payload[j] == '\\'; j-- {
			slashes++
		}
		if slashes%2 == 0 {
			return at
		}
		end := i + 2
		if payload[i+1] == 'u' {
			end = i + 6
		}
		if end > at {
			return end
		}
		return at
	}
	return at
}
