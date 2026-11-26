package llm

import (
	"sync/atomic"
	"time"
)

// DefaultNetworkWait is how long a model call waits for a lost network
// connection (cable to Wi-Fi switch, sleep, VPN reconnect) to come back
// before the run stops with a resumable network stop.
const DefaultNetworkWait = 10 * time.Minute

var networkWait atomic.Int64

func init() { networkWait.Store(int64(DefaultNetworkWait)) }

// SetNetworkWait installs config.toml network_wait_minutes (<= 0: default).
func SetNetworkWait(d time.Duration) {
	if d <= 0 {
		d = DefaultNetworkWait
	}
	networkWait.Store(int64(d))
}

// NetworkWait is the current wait for a lost connection.
func NetworkWait() time.Duration { return time.Duration(networkWait.Load()) }
