package llm

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// maxSSELineBytes bounds one SSE line. A bufio.Scanner capped at 1 MiB used
// to fail a whole response with "token too long" whenever a tool call carried
// more than ~1 MiB of arguments on one line (the Responses API always echoes
// the full arguments in function_call_arguments.done / output_item.done /
// response.completed; many Chat servers send a call in one chunk). Tool-call
// arguments are capped far lower (maxToolCallArgumentBytes) before a line can
// grow this large through deltas; this cap only stops a broken upstream from
// growing one line without bound.
const maxSSELineBytes = 64 << 20

var errSSELineTooLong = fmt.Errorf("sse: line longer than %d MiB", maxSSELineBytes>>20)

// sseLineReader reads newline-terminated lines of any length up to
// maxSSELineBytes. A trailing "\r" is removed like bufio.ScanLines does; a
// final line without a newline is returned before io.EOF.
type sseLineReader struct {
	r   *bufio.Reader
	buf []byte
}

func newSSELineReader(r io.Reader) *sseLineReader {
	return &sseLineReader{r: bufio.NewReaderSize(r, 64*1024)}
}

// next returns the next line, io.EOF at a clean end of input, or the read error.
func (s *sseLineReader) next() (string, error) {
	if cap(s.buf) > 1<<20 {
		s.buf = nil // do not pin a one-off huge line for the rest of the stream
	}
	s.buf = s.buf[:0]
	for {
		chunk, err := s.r.ReadSlice('\n')
		if len(s.buf)+len(chunk) > maxSSELineBytes {
			return "", errSSELineTooLong
		}
		s.buf = append(s.buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(s.buf) > 0 {
				return trimSSELine(s.buf), nil
			}
			return "", err
		}
		return trimSSELine(s.buf), nil
	}
}

func trimSSELine(b []byte) string {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return string(b)
}
