//go:build windows

package unified_exec

import (
	"strings"
	"testing"
)

func TestSpawnWindowsSandboxLiveSessionForLevelRejectsManagedNetworkingWithLegacyLevel(t *testing.T) {
	_, err := SpawnWindowsSandboxLiveSessionForLevel(&WindowsSandboxSessionRequest{
		WindowsSandboxLevel: WindowsSandboxLevelLegacy,
		ProxyEnforced:       true,
	})
	if err == nil || !strings.Contains(err.Error(), "managed networking requires the elevated Windows sandbox backend") {
		t.Fatalf("SpawnWindowsSandboxLiveSessionForLevel(legacy, proxy) error = %v, want managed networking rejection", err)
	}
}
