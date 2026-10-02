package webgui

import "io"

// Keep Flush in the caller so each complete event is still emitted immediately.
// JSON is already escaped; writing its framing separately avoids a second
// payload-sized formatting buffer.
func writeSSEFrame(w io.Writer, data []byte) {
	if n, err := io.WriteString(w, "data: "); err != nil || n != len("data: ") {
		return
	}
	if n, err := w.Write(data); err != nil || n != len(data) {
		return
	}
	_, _ = io.WriteString(w, "\n\n")
}
