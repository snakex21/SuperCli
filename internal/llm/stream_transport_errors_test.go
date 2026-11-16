package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWrapStreamErrorClassifiesTransportDrops(t *testing.T) {
	urlErr := func(err error) error { return &url.Error{Op: "Post", URL: "http://x/v1/chat/completions", Err: err} }
	transient := []error{
		urlErr(io.EOF),
		io.ErrUnexpectedEOF,
		fmt.Errorf("read: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}),
		errors.New("stream error: stream ID 7; INTERNAL_ERROR; received from peer"),
		errors.New("http2: server sent GOAWAY and closed the connection"),
		errors.New("wsarecv: An existing connection was forcibly closed by the remote host."),
	}
	for _, cause := range transient {
		err := wrapStreamError("sse", cause)
		var transport *StreamTransportError
		if !errors.As(err, &transport) || ClassifyProviderError(err) != RetryTransient {
			t.Errorf("%v: not a transient StreamTransportError (%T, %s)", cause, err, ClassifyProviderError(err))
		}
		if err.Error() != "sse: "+cause.Error() {
			t.Errorf("text changed: %q", err.Error())
		}
		if !errors.Is(err, cause) {
			t.Errorf("%v: cause lost", cause)
		}
	}
	notTransport := []error{
		context.Canceled,
		urlErr(context.Canceled),
		urlErr(&net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}),
		urlErr(&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}),
		&ProviderStreamError{Prefix: "provider stream error", Message: "upstream said: unexpected EOF"},
		&RunawayToolCallError{Tool: "write_file", KB: 16},
		fmt.Errorf("%w: bad json", errSSEUnexpected),
	}
	for _, cause := range notTransport {
		err := wrapStreamError("http", cause)
		if IsStreamTransportFailure(err) {
			t.Errorf("%v: wrongly typed as transport failure", cause)
		}
	}
	if ClassifyProviderError(wrapStreamError("http", urlErr(context.Canceled))) != RetryPermanent {
		t.Fatal("a user cancel must stay permanent")
	}
}

func TestIdleTimeoutReaderReportsTypedTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-ctx.Done()
		pw.CloseWithError(context.Canceled) // what the cancelled request body returns
	}()
	r := newIdleTimeoutReader(pr, 50*time.Millisecond, cancel)
	defer r.Close()
	_, err := io.ReadAll(r)
	var idle *StreamIdleTimeoutError
	if !errors.As(err, &idle) || !errors.Is(err, ErrStreamIdleTimeout) || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %T %v", err, err)
	}
	if idle.Idle != 50*time.Millisecond || !strings.Contains(err.Error(), "no data from provider for 50ms") {
		t.Fatalf("message = %q", err)
	}
	if ClassifyProviderError(wrapStreamError("sse", err)) != RetryTransient {
		t.Fatal("idle timeout must be transient")
	}

	// A user cancel (watchdog not fired) stays context.Canceled.
	pr2, pw2 := io.Pipe()
	r2 := newIdleTimeoutReader(pr2, time.Hour, func() {})
	pw2.CloseWithError(context.Canceled)
	if _, err := io.ReadAll(r2); !errors.Is(err, context.Canceled) {
		t.Fatalf("user cancel = %v", err)
	}
	r2.Close()
}

func TestHeaderTimeoutIsTypedAndKeepsText(t *testing.T) {
	err := error(&StreamIdleTimeoutError{Idle: 700 * time.Millisecond, Headers: true})
	if got := wrapStreamError("http", err).Error(); got != "http: response headers timeout after 700ms" {
		t.Fatalf("text = %q", got)
	}
	if ClassifyProviderError(err) != RetryTransient {
		t.Fatal("header timeout must be transient")
	}
}

func TestSSELineReaderLongLinesAndEndings(t *testing.T) {
	long := strings.Repeat("y", 3<<20)
	in := "a\r\nb\n" + long + "\nlast"
	r := newSSELineReader(&hardChunkReader{data: []byte(in)})
	var got []string
	for {
		line, err := r.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, line)
	}
	if len(got) != 4 || got[0] != "a" || got[1] != "b" || got[2] != long || got[3] != "last" {
		t.Fatalf("lines = %d %q %q", len(got), got[0], got[len(got)-1])
	}
	// A read error mid-line is reported, not swallowed.
	r = newSSELineReader(io.MultiReader(strings.NewReader("partial"), iotestErrReader{}))
	if _, err := r.next(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("mid-line error = %v", err)
	}
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
