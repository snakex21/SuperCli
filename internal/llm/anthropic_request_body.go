package llm

import (
	"bytes"
	"io"
)

// Like http.NewRequest's bytes.Reader replay, but recognizes locally owned
// encoded bytes for metadata hashing without cloning the entire body.
type anthropicOwnedRequestBody struct {
	reader *bytes.Reader
	data   []byte
}

func (r *anthropicOwnedRequestBody) Read(p []byte) (int, error)         { return r.reader.Read(p) }
func (r *anthropicOwnedRequestBody) WriteTo(w io.Writer) (int64, error) { return r.reader.WriteTo(w) }
func (r *anthropicOwnedRequestBody) Close() error                       { return nil }

func anthropicOwnedGetBody(data []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return &anthropicOwnedRequestBody{reader: bytes.NewReader(data), data: data}, nil
	}
}

func anthropicReplayRequestBytes(reader io.ReadCloser) ([]byte, error) {
	if own, ok := reader.(*anthropicOwnedRequestBody); ok && own.reader != nil && own.reader.Size() == int64(len(own.data)) {
		remaining := own.reader.Len()
		view := own.data[len(own.data)-remaining:]
		// Preserve io.ReadAll consumption and already-exhausted cursor state.
		if remaining > 0 {
			_, _ = own.reader.Seek(0, io.SeekEnd)
		}
		if view == nil {
			view = []byte{}
		}
		return view, nil
	}
	return io.ReadAll(reader)
}
