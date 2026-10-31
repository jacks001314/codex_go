//go:build windows

package win

import (
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
)

// TestRegisteredRuntimeRejectsHelperOwnedProvisioningLikeRust pins Rust #45550:
// a registered Core's managed accounts belong to the packaged service, so the
// helper refuses to provision them but still allows refresh-only and read-ACL
// passes.
func TestRegisteredRuntimeRejectsHelperOwnedProvisioningLikeRust(t *testing.T) {
	registered := string(windowssandbox.SetupRuntimeRegistered)
	cases := []struct {
		name    string
		payload setupPayload
		wantErr bool
	}{
		{name: "registered full", payload: setupPayload{Runtime: registered, Mode: setupModeFull}, wantErr: true},
		{name: "registered provision", payload: setupPayload{Runtime: registered, Mode: setupModeProvision}, wantErr: true},
		{name: "registered interactive provision", payload: setupPayload{Runtime: registered, Mode: setupModeInteractiveProvision}, wantErr: true},
		{name: "registered refresh", payload: setupPayload{Runtime: registered, Mode: setupModeFull, RefreshOnly: true}},
		{name: "registered read acls", payload: setupPayload{Runtime: registered, Mode: setupModeReadACLsOnly}},
		{name: "legacy full", payload: setupPayload{Mode: setupModeFull}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSetupRuntimeForProvisioning(&tc.payload)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSetupRuntimeForProvisioning() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "registered Core requires service-owned provisioning") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if err := validateSetupRuntimeForProvisioning(nil); err != nil {
		t.Fatalf("nil payload error = %v", err)
	}
}

func TestSetupModeProvisionsAccountsLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		mode        setupMode
		refreshOnly bool
		want        bool
	}{
		{"full", setupModeFull, false, true},
		{"provision-only", setupModeProvision, false, true},
		{"interactive-provision", setupModeInteractiveProvision, false, true},
		{"interactive-provision refresh", setupModeInteractiveProvision, true, true},
		{"refresh", setupModeFull, true, false},
		{"read-acls-only", setupModeReadACLsOnly, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := setupModeProvisionsAccounts(tc.mode, tc.refreshOnly); got != tc.want {
				t.Fatalf("setupModeProvisionsAccounts(%q, %t) = %t, want %t", tc.mode, tc.refreshOnly, got, tc.want)
			}
		})
	}
}

// TestSetupPayloadAcceptsInteractiveProvisionModeLikeRust pins Rust #42309:
// the helper accepts the provisioning-only mode used by elevated interactive
// setup.
func TestSetupPayloadAcceptsInteractiveProvisionModeLikeRust(t *testing.T) {
	encoded, err := encodeSetupPayload(&setupPayload{
		Version:   windowssandbox.SetupVersion,
		CodexHome: t.TempDir(),
		Mode:      setupModeInteractiveProvision,
	})
	if err != nil {
		t.Fatalf("encodeSetupPayload() error = %v", err)
	}
	payload, err := payloadFromArgs([]string{encoded})
	if err != nil {
		t.Fatalf("payloadFromArgs() error = %v", err)
	}
	if payload.Mode != setupModeInteractiveProvision {
		t.Fatalf("payload.Mode = %q, want %q", payload.Mode, setupModeInteractiveProvision)
	}
}
