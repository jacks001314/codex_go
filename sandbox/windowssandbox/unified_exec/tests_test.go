package unified_exec

import (
	"strings"
	"testing"
)

func TestSpawnWindowsSandboxSessionForLevelPTYUsesNormalRequestValidation(t *testing.T) {
	_, err := SpawnWindowsSandboxSessionForLevel(&WindowsSandboxSessionRequest{PTY: true})
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("SpawnWindowsSandboxSessionForLevel(PTY) error = %v, want capture validation error", err)
	}
}

func TestSpawnWindowsSandboxSessionForLevelRejectsNilRequest(t *testing.T) {
	_, err := SpawnWindowsSandboxSessionForLevel(nil)
	if err == nil {
		t.Fatalf("SpawnWindowsSandboxSessionForLevel(nil) error = nil, want invalid request")
	}
}

func TestSpawnWindowsSandboxSessionForLevelRejectsManagedNetworkingWithLegacyLevel(t *testing.T) {
	_, err := SpawnWindowsSandboxSessionForLevel(&WindowsSandboxSessionRequest{
		WindowsSandboxLevel: WindowsSandboxLevelLegacy,
		ProxyEnforced:       true,
	})
	if err == nil || !strings.Contains(err.Error(), "managed networking requires the elevated Windows sandbox backend") {
		t.Fatalf("SpawnWindowsSandboxSessionForLevel(legacy, proxy) error = %v, want managed networking rejection", err)
	}
}
