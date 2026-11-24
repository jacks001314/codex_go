//go:build unix

package mcp

import (
	"fmt"
	"path/filepath"

	"github.com/google/uuid"

	"codex_go/codexuds"
)

// desktopSocketPath resolves the Desktop app's shared socket for one id,
// mirroring Rust's prepare_shared_daemon_socket_directory().join(format!
// ("desktop-{id}.sock")).
func desktopSocketPath(id uuid.UUID) (string, error) {
	directory, err := codexuds.PrepareSharedDaemonSocketDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, fmt.Sprintf("desktop-%s.sock", id.String())), nil
}
