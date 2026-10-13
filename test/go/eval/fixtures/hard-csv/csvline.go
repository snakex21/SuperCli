// Package csvline parses a small CSV dialect without using encoding/csv.
package csvline

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSyntax is wrapped by every parse error.
var ErrSyntax = errors.New("csvline: syntax error")

// ParseError reports the 1-based index of the record that failed to parse.
type ParseError struct {
	Record int
	Err    error
}

func (e *ParseError) Error() string { return fmt.Sprintf("record %d: %v", e.Record, e.Err) }

func (e *ParseError) Unwrap() error { return e.Err }

// Parse splits input into records and fields.
func Parse(input string) ([][]string, error) {
	// TODO: quoting, CRLF and error handling are not implemented yet.
	var records [][]string
	for _, line := range strings.Split(strings.TrimSuffix(input, "\n"), "\n") {
		if input == "" {
			break
		}
		records = append(records, strings.Split(line, ","))
	}
	return records, nil
}
