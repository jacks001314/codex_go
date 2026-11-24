//go:build !unix

package mcp

import (
	"errors"

	"github.com/google/uuid"
)

// desktopSocketPath reports that this platform has no shared Desktop socket
// directory. Rust #63497 also routes Desktop endpoints only on Unix; other
// platforms reject the endpoint when its socket cannot be resolved.
func desktopSocketPath(uuid.UUID) (string, error) {
	return "", errors.New("Desktop MCP sockets are unavailable on this platform")
}
