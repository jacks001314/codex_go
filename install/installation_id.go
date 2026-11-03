package install

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gofrs/flock"
)

const InstallationIDFilename = "installation_id"

// installationIDLockPath is the sidecar that coordinates first-use creation.
// The ID itself stays readable by ordinary tools; the lock only serializes the
// read-upgrade-write sequence (Rust #59021, core/src/installation_id.rs).
func installationIDLockPath(path string) string {
	return path + ".lock"
}

// ResolveInstallationID returns the Codex installation UUID, creating it on
// first use. Valid IDs need only a shared lock, so concurrent readers do not
// serialize. A missing or invalid ID upgrades to an exclusive lock and rereads
// before generating, which keeps concurrent starters on one ID.
func ResolveInstallationID(codexHome string) (string, error) {
	path := filepath.Join(codexHome, InstallationIDFilename)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return "", err
	}
	lock, err := openInstallationIDLock(installationIDLockPath(path))
	if err != nil {
		return "", err
	}
	if err := lock.RLock(); err != nil {
		_ = lock.Unlock()
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = lock.Unlock()
		return "", err
	}
	if existing := normalizeUUID(strings.TrimSpace(string(data))); existing != "" {
		unlockErr := lock.Unlock()
		// Rust narrows an existing permissive ID to 0644 while holding the lock.
		if runtime.GOOS != "windows" {
			if err := os.Chmod(path, 0o644); err != nil {
				return "", err
			}
		}
		if unlockErr != nil {
			return "", unlockErr
		}
		return existing, nil
	}
	if err := lock.Unlock(); err != nil {
		return "", err
	}

	if err := lock.Lock(); err != nil {
		return "", err
	}
	defer func() { _ = lock.Unlock() }()
	// Another process can initialize the ID between the lock upgrade and now.
	data, err = os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if existing := normalizeUUID(strings.TrimSpace(string(data))); existing != "" {
		if runtime.GOOS != "windows" {
			if err := os.Chmod(path, 0o644); err != nil {
				return "", err
			}
		}
		return existing, nil
	}
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id), 0o644); err != nil {
		return "", err
	}
	return id, nil
}

// openInstallationIDLock prepares a one-byte sidecar because some platforms
// cannot lock a zero-length range. The lock handle is returned by flock.New.
func openInstallationIDLock(path string) (*flock.Flock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, statErr
	}
	if info.Size() == 0 {
		if _, err := file.WriteString("\n"); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return flock.New(path), nil
}

func normalizeUUID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 36 {
		return ""
	}
	for i, char := range value {
		switch i {
		case 8, 13, 18, 23:
			if char != '-' {
				return ""
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return ""
			}
		}
	}
	return value
}

func newUUID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
