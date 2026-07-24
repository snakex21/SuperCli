package llm

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// doWithResponseHeaderTimeout bounds only the wait for HTTP response headers.
// The timer is stopped as soon as Client.Do returns, so generation may stream
// for arbitrarily long as long as the idle watchdog keeps seeing data. Passing
// the request's cancel function also makes this work with caller-supplied HTTP
// clients whose transports do not expose ResponseHeaderTimeout.
func doWithResponseHeaderTimeout(client *http.Client, req *http.Request, timeout time.Duration, cancel func()) (*http.Response, error) {
	if timeout <= 0 || cancel == nil {
		return client.Do(req)
	}
	var state struct {
		sync.Mutex
		finished bool
		timedOut bool
	}
	timer := time.AfterFunc(timeout, func() {
		state.Lock()
		if state.finished {
			state.Unlock()
			return
		}
		state.timedOut = true
		state.Unlock()
		cancel()
	})
	resp, err := client.Do(req)
	state.Lock()
	state.finished = true
	timedOut := state.timedOut
	state.Unlock()
	timer.Stop()
	if timedOut {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("response headers timeout after %s", timeout)
	}
	return resp, err
}
