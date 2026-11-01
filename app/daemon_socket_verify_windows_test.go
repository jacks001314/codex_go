//go:build windows

package app

import (
	"testing"

	"codex_go/appserver"
)

// assertVerifiedDefaultSocket mirrors the Windows contract: the rendezvous
// directory DACL is what makes the socket the daemon's.
func assertVerifiedDefaultSocket(t *testing.T, socketPath string) {
	t.Helper()
	if _, verified := appserver.VerifiedLocalDaemonSocket(socketPath); !verified {
		t.Fatalf("VerifiedLocalDaemonSocket(%q) is unverified after preparing the socket directory", socketPath)
	}
}
