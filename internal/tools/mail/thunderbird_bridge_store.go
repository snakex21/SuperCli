package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// thunderbirdPairing is the one Thunderbird profile this host trusts. A host
// deliberately keeps a single pairing: two paired profiles polling the same
// queue would let a request run in whichever mailbox polled first.
type thunderbirdPairing struct {
	ID               string    `json:"id"`
	Key              []byte    `json:"-"`
	Origin           string    `json:"origin,omitempty"`
	PairedAt         time.Time `json:"paired_at"`
	ExtensionVersion string    `json:"extension_version,omitempty"`
}

type thunderbirdStoreFile struct {
	Version int    `json:"version"`
	HostID  string `json:"host_id"`
	Pairing *struct {
		ID               string    `json:"id"`
		ProtectedKey     string    `json:"protected_key"`
		Origin           string    `json:"origin,omitempty"`
		PairedAt         time.Time `json:"paired_at"`
		ExtensionVersion string    `json:"extension_version,omitempty"`
	} `json:"pairing,omitempty"`
}

// thunderbirdStore persists the host identity and the pairing secret below the
// application data root. The secret is wrapped with DPAPI on Windows (current
// user) and the file and directory are restricted to the current account.
type thunderbirdStore struct {
	dir string
}

func thunderbirdStoreFor(root string) thunderbirdStore {
	return thunderbirdStore{dir: filepath.Join(root, "thunderbird-bridge")}
}

func (s thunderbirdStore) path() string { return filepath.Join(s.dir, "pairing.json") }

// load returns the host id (created on first use) and the current pairing, if
// any. A pairing whose key cannot be unwrapped (data folder copied to another
// Windows account or machine) is reported as unreadable, never used.
func (s thunderbirdStore) load() (hostID string, pairing *thunderbirdPairing, err error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		hostID = thunderbirdRandomID(12)
		return hostID, nil, s.save(hostID, nil)
	}
	if err != nil {
		return "", nil, err
	}
	var file thunderbirdStoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		return "", nil, fmt.Errorf("thunderbird bridge pairing file is damaged: %w", err)
	}
	hostID = strings.TrimSpace(file.HostID)
	if hostID == "" {
		hostID = thunderbirdRandomID(12)
		if err := s.save(hostID, nil); err != nil {
			return "", nil, err
		}
	}
	if file.Pairing == nil {
		return hostID, nil, nil
	}
	key, err := thunderbirdUnprotectSecret(file.Pairing.ProtectedKey)
	if err != nil || len(key) != 32 {
		return hostID, nil, errThunderbirdPairingUnreadable
	}
	return hostID, &thunderbirdPairing{
		ID:               file.Pairing.ID,
		Key:              key,
		Origin:           file.Pairing.Origin,
		PairedAt:         file.Pairing.PairedAt,
		ExtensionVersion: file.Pairing.ExtensionVersion,
	}, nil
}

var errThunderbirdPairingUnreadable = errors.New("the saved Thunderbird pairing cannot be read on this Windows account; pair Thunderbird again")

func (s thunderbirdStore) save(hostID string, pairing *thunderbirdPairing) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	_ = thunderbirdRestrictPath(s.dir)
	file := thunderbirdStoreFile{Version: 1, HostID: hostID}
	if pairing != nil {
		protected, err := thunderbirdProtectSecret(pairing.Key)
		if err != nil {
			return fmt.Errorf("protect pairing secret: %w", err)
		}
		file.Pairing = &struct {
			ID               string    `json:"id"`
			ProtectedKey     string    `json:"protected_key"`
			Origin           string    `json:"origin,omitempty"`
			PairedAt         time.Time `json:"paired_at"`
			ExtensionVersion string    `json:"extension_version,omitempty"`
		}{pairing.ID, protected, pairing.Origin, pairing.PairedAt.UTC(), pairing.ExtensionVersion}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "pairing-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	// Restrict before the secret is written, so it is never readable by others.
	_ = thunderbirdRestrictPath(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path()); err != nil {
		return err
	}
	return thunderbirdRestrictPath(s.path())
}
