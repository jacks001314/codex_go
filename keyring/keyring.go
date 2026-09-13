// Package keyring provides the shared OS credential-store abstraction used by
// CLI auth and MCP OAuth credential storage. It mirrors Rust's
// `codex-rs/keyring-store` crate (load/save/delete over a service/account pair):
// Windows uses the Windows Credential Manager; hosts without a supported
// backend report unavailable so callers fall back to the credentials file.
package keyring

import (
	"errors"
	"strings"
	"unicode/utf16"
)

// ErrNotFound reports that no credential exists for the service/account pair.
var ErrNotFound = errors.New("secret not found")

// ErrUnavailable reports that this host has no supported OS keyring backend.
var ErrUnavailable = errors.New("OS keyring storage is unavailable")

// Store is the minimal keyring surface (Rust's KeyringStore trait).
type Store interface {
	Load(service string, account string) (string, error)
	Save(service string, account string, secret string) error
	Delete(service string, account string) (bool, error)
}

// Available reports whether this build has a durable OS keyring backend.
func Available() bool {
	return osKeyringAvailable
}

// New returns the platform keyring store. It returns a store whose methods
// report ErrUnavailable when the host has no supported backend.
func New() Store {
	return osKeyringStore{}
}

// WindowsTargetName mirrors the keyring crate's Windows mapping: a credential
// is identified by the single target name "<account>.<service>".
func WindowsTargetName(service string, account string) string {
	return account + "." + service
}

// EncodeWindowsSecret mirrors the keyring crate: the password is stored as a
// little-endian UTF-16 blob (no null terminator) for native Windows interop.
func EncodeWindowsSecret(secret string) []byte {
	encoded := utf16.Encode([]rune(secret))
	blob := make([]byte, len(encoded)*2)
	for index, unit := range encoded {
		blob[index*2] = byte(unit)
		blob[index*2+1] = byte(unit >> 8)
	}
	return blob
}

// DecodeWindowsSecret reverses EncodeWindowsSecret. An odd-length blob or an
// invalid UTF-16 sequence is rejected like the keyring crate.
func DecodeWindowsSecret(blob []byte) (string, error) {
	if len(blob)%2 != 0 {
		return "", errors.New("credential blob has an odd byte length")
	}
	units := make([]uint16, len(blob)/2)
	for index := range units {
		units[index] = uint16(blob[index*2]) | uint16(blob[index*2+1])<<8
	}
	return string(utf16.Decode(units)), nil
}

// requireIdentifier rejects empty service/account values, which would collide on
// the concatenated Windows target name.
func requireIdentifier(service string, account string) error {
	if strings.TrimSpace(service) == "" {
		return errors.New("keyring service is required")
	}
	if strings.TrimSpace(account) == "" {
		return errors.New("keyring account is required")
	}
	return nil
}
