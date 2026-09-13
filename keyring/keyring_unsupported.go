//go:build !windows

package keyring

// Go links a native keyring backend only on Windows. On other hosts the store
// reports unavailable so callers fall back to the credentials file (Rust uses
// the macOS Keychain and Linux Secret Service; porting those needs either cgo
// or a CLI that would expose secrets in the process arguments).
const osKeyringAvailable = false

type osKeyringStore struct{}

func (osKeyringStore) Load(service string, account string) (string, error) {
	return "", ErrUnavailable
}

func (osKeyringStore) Save(service string, account string, secret string) error {
	return ErrUnavailable
}

func (osKeyringStore) Delete(service string, account string) (bool, error) {
	return false, ErrUnavailable
}
