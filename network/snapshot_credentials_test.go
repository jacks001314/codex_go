package network

import (
	"strings"
	"testing"
)

func TestSnapshotCredentialMaskAndRestoreLikeRust(t *testing.T) {
	pairs := []CredentialMaskPair{{Real: "sk-real-secret", Dummy: "sk-dummy-value"}}
	masked := MaskSnapshotText(`export TOKEN="sk-real-secret"`, pairs)
	if strings.Contains(masked, "sk-real-secret") || !strings.Contains(masked, "sk-dummy-value") {
		t.Fatalf("masked snapshot = %q", masked)
	}
	env := map[string]string{
		"TOKEN":                      "sk-dummy-value",
		CredentialBrokerActiveEnvKey: "1",
		BrokeredCredentialsEnvKey:    `[["TOKEN","sk-dummy-value"]]`,
	}
	RestoreSnapshotCredentials(env, pairs)
	if env["TOKEN"] != "sk-real-secret" {
		t.Fatalf("restored token = %q", env["TOKEN"])
	}
	if _, ok := env[CredentialBrokerActiveEnvKey]; ok {
		t.Fatal("broker marker survived fallback restore")
	}
	if _, ok := env[BrokeredCredentialsEnvKey]; ok {
		t.Fatal("brokered credential marker survived fallback restore")
	}
}
