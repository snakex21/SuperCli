package mail

import (
	"errors"
	"fmt"
	"time"
)

// ThunderbirdBridgeStatus is the host side of the bridge as shown by the GUI,
// the TUI and NestCafe. It never contains the pairing secret or code key.
type ThunderbirdBridgeStatus struct {
	Protocol           int        `json:"protocol"`
	Addr               string     `json:"addr"`
	HostID             string     `json:"host_id"`
	Listening          bool       `json:"listening"`
	PortError          string     `json:"port_error,omitempty"`
	Paired             bool       `json:"paired"`
	PairedAt           *time.Time `json:"paired_at,omitempty"`
	ExtensionVersion   string     `json:"extension_version,omitempty"`
	PairingProblem     string     `json:"pairing_problem,omitempty"`
	PairingActive      bool       `json:"pairing_active"`
	PairingExpiresAt   *time.Time `json:"pairing_expires_at,omitempty"`
	Connected          bool       `json:"connected"`
	LastSeen           *time.Time `json:"last_seen,omitempty"`
	LegacyExtension    bool       `json:"legacy_extension"`
	LastRejected       *time.Time `json:"last_rejected,omitempty"`
	LastRejectedReason string     `json:"last_rejected_reason,omitempty"`
	State              string     `json:"state"`
	Message            string     `json:"message"`
}

// ThunderbirdPairingCode is shown to the user once; it is valid for five
// minutes and a few attempts.
type ThunderbirdPairingCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	HostID    string    `json:"host_id"`
}

const thunderbirdRecent = 30 * time.Second

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ThunderbirdBridgeStatusFor reports pairing and connection state. When the
// host is paired it also claims the bridge port (the extension can only
// connect to a listening host).
func ThunderbirdBridgeStatusFor(dataDir string) ThunderbirdBridgeStatus {
	b := globalThunderbirdBridge
	if err := b.configureDataDir(dataDir); err != nil {
		return ThunderbirdBridgeStatus{Protocol: thunderbirdProtocolVersion, Addr: b.listenAddr(), State: "error", Message: err.Error()}
	}
	listenErr := b.ensureServer()
	return b.status(listenErr)
}

func (b *thunderbirdBridgeState) status(listenErr error) ThunderbirdBridgeStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	now := time.Now()
	st := ThunderbirdBridgeStatus{
		Protocol:  thunderbirdProtocolVersion,
		Addr:      b.listenAddr(),
		HostID:    a.hostID,
		Listening: b.listener != nil,
	}
	var busy *thunderbirdPortBusyError
	if errors.As(listenErr, &busy) {
		st.PortError = listenErr.Error()
	}
	if a.pairing != nil {
		st.Paired = true
		st.PairedAt = timePtr(a.pairing.PairedAt)
		st.ExtensionVersion = a.pairing.ExtensionVersion
	} else if a.loadErr != nil {
		st.PairingProblem = a.loadErr.Error()
	}
	if a.window != nil && now.Before(a.window.expires) {
		st.PairingActive = true
		st.PairingExpiresAt = timePtr(a.window.expires)
	}
	st.Connected = st.Listening && st.Paired && (b.activePolls > 0 || (!b.lastPoll.IsZero() && now.Sub(b.lastPoll) <= thunderbirdRecent))
	st.LastSeen = timePtr(b.lastPoll)
	st.LegacyExtension = !a.legacySeen.IsZero() && now.Sub(a.legacySeen) <= 2*time.Minute
	if !a.rejectedAt.IsZero() && now.Sub(a.rejectedAt) <= 2*time.Minute {
		st.LastRejected = timePtr(a.rejectedAt)
		st.LastRejectedReason = a.rejectedReason
	}
	switch {
	case st.PortError != "":
		st.State, st.Message = "port_busy", st.PortError
	case st.PairingActive:
		st.State, st.Message = "pairing", "Enter the code in Thunderbird: Add-ons -> Thunderbird AI Bridge -> Options -> Pair."
	case st.PairingProblem != "":
		st.State, st.Message = "unpaired", st.PairingProblem
	case !st.Paired:
		st.State, st.Message = "unpaired", "Not paired. Pair this SuperCli with Thunderbird AI Bridge 0.10.0 or newer."
	case st.Connected:
		st.State, st.Message = "connected", "Paired and connected."
	default:
		st.State, st.Message = "disconnected", b.notConnectedLocked().Error()
	}
	return st
}

func (b *thunderbirdBridgeState) notConnectedError() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notConnectedLocked()
}

func (b *thunderbirdBridgeState) notConnectedLocked() error {
	a := b.authLocked()
	now := time.Now()
	if !a.legacySeen.IsZero() && now.Sub(a.legacySeen) <= thunderbirdRecent {
		return errors.New("Thunderbird Bridge is not connected: Thunderbird runs an outdated Thunderbird AI Bridge (0.9.x) that cannot authenticate, so it was refused. Update the extension to 0.10.0 or newer and pair it with this SuperCli. Nothing was sent to Thunderbird")
	}
	if !a.rejectedAt.IsZero() && now.Sub(a.rejectedAt) <= thunderbirdRecent {
		return fmt.Errorf("Thunderbird Bridge is not connected: a Thunderbird extension tried to connect but was refused (%s). If it is your Thunderbird, pair it again with this SuperCli. Nothing was sent to Thunderbird", a.rejectedReason)
	}
	return errors.New("Thunderbird Bridge is not connected: start Thunderbird with Thunderbird AI Bridge 0.10.0 or newer paired with this SuperCli (Add-ons -> Thunderbird AI Bridge -> Options shows the connection)")
}

// StartThunderbirdPairing opens a five-minute pairing window and returns the
// code to show to the user. It claims the bridge port; when another instance
// holds it, pairing is refused with an explanation.
func StartThunderbirdPairing(dataDir string) (ThunderbirdPairingCode, error) {
	b := globalThunderbirdBridge
	if err := b.configureDataDir(dataDir); err != nil {
		return ThunderbirdPairingCode{}, err
	}
	return b.startPairing()
}

func (b *thunderbirdBridgeState) startPairing() (ThunderbirdPairingCode, error) {
	code := thunderbirdNewPairingCode()
	b.mu.Lock()
	a := b.authLocked()
	if a.loadErr != nil && !errors.Is(a.loadErr, errThunderbirdPairingUnreadable) {
		err := a.loadErr
		b.mu.Unlock()
		return ThunderbirdPairingCode{}, fmt.Errorf("cannot pair: %w", err)
	}
	expires := time.Now().Add(thunderbirdPairingWindow)
	a.window = &thunderbirdPairingWindowState{codeKey: thunderbirdCodeKey(code), expires: expires, startedAt: time.Now()}
	hostID := a.hostID
	b.mu.Unlock()
	if err := b.ensureServer(); err != nil {
		b.mu.Lock()
		b.auth.window = nil
		b.mu.Unlock()
		return ThunderbirdPairingCode{}, err
	}
	return ThunderbirdPairingCode{Code: thunderbirdFormatPairingCode(code), ExpiresAt: expires, HostID: hostID}, nil
}

// CancelThunderbirdPairing closes an open pairing window.
func CancelThunderbirdPairing(dataDir string) error {
	b := globalThunderbirdBridge
	if err := b.configureDataDir(dataDir); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	a.window = nil
	if a.pairing == nil {
		b.stopServerLocked()
	}
	return nil
}

// UnpairThunderbird forgets the pairing secret, ends every session and frees
// the bridge port. The extension then fails closed until paired again.
func UnpairThunderbird(dataDir string) error {
	b := globalThunderbirdBridge
	if err := b.configureDataDir(dataDir); err != nil {
		return err
	}
	return b.unpair()
}

func (b *thunderbirdBridgeState) unpair() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	if err := b.savePairingLocked(nil); err != nil {
		return err
	}
	a.window = nil
	a.challenges = make(map[string]time.Time)
	b.lastPoll = time.Time{}
	b.stopServerLocked()
	return nil
}
