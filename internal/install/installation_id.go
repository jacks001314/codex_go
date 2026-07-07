package install

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const InstallationIDFilename = "installation_id"

func ResolveInstallationID(codexHome string) (string, error) {
	path := filepath.Join(codexHome, InstallationIDFilename)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if existing := normalizeUUID(strings.TrimSpace(string(data))); existing != "" {
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
