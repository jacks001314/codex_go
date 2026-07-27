package windowssandbox

import (
	"errors"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestSandboxSetupIsCompleteRequiresMarkerAndUsersVersion(t *testing.T) {
	home := t.TempDir()
	complete, err := SandboxSetupIsComplete(home)
	if err != nil {
		t.Fatalf("SandboxSetupIsComplete() error = %v", err)
	}
	if complete {
		t.Fatalf("setup unexpectedly complete without files")
	}
	if err := WriteSetupMarker(home, &SetupMarker{Version: SetupVersion, OfflineUsername: OfflineUsername, OnlineUsername: OnlineUsername}); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	complete, err = SandboxSetupIsComplete(home)
	if err != nil {
		t.Fatalf("SandboxSetupIsComplete(marker only) error = %v", err)
	}
	if complete {
		t.Fatalf("setup unexpectedly complete without users file")
	}
	users := &SandboxUsersFile{
		Version: SetupVersion,
		Offline: SandboxUserRecord{
			Username: OfflineUsername,
			Password: "encrypted",
		},
		Online: SandboxUserRecord{
			Username: OnlineUsername,
			Password: "encrypted",
		},
	}
	if err := WriteSandboxUsersFile(home, users); err != nil {
		t.Fatalf("WriteSandboxUsersFile() error = %v", err)
	}
	complete, err = SandboxSetupIsComplete(home)
	if err != nil {
		t.Fatalf("SandboxSetupIsComplete(complete) error = %v", err)
	}
	if !complete {
		t.Fatalf("setup should be complete")
	}
	users.Version = SetupVersion - 1
	if err := WriteSandboxUsersFile(home, users); err != nil {
		t.Fatalf("WriteSandboxUsersFile(stale) error = %v", err)
	}
	complete, err = SandboxSetupIsComplete(home)
	if err != nil {
		t.Fatalf("SandboxSetupIsComplete(stale) error = %v", err)
	}
	if complete {
		t.Fatalf("setup unexpectedly complete with stale users file")
	}
}

func TestRemoveSandboxUsersFileIgnoresMissingFile(t *testing.T) {
	home := t.TempDir()
	if err := RemoveSandboxUsersFile(home); err != nil {
		t.Fatalf("RemoveSandboxUsersFile(missing) error = %v", err)
	}
	if err := WriteSandboxUsersFile(home, &SandboxUsersFile{Version: SetupVersion}); err != nil {
		t.Fatalf("WriteSandboxUsersFile() error = %v", err)
	}
	if err := RemoveSandboxUsersFile(home); err != nil {
		t.Fatalf("RemoveSandboxUsersFile() error = %v", err)
	}
}

func TestSelectSandboxCredentialsMissingSetupReturnsNil(t *testing.T) {
	home := t.TempDir()
	creds, err := SelectSandboxCredentials(home, SandboxNetworkIdentityOffline)
	if err != nil {
		t.Fatalf("SelectSandboxCredentials(missing marker) error = %v", err)
	}
	if creds != nil {
		t.Fatalf("SelectSandboxCredentials(missing marker) = %#v, want nil", creds)
	}
	if err := WriteSetupMarker(home, &SetupMarker{Version: SetupVersion, OfflineUsername: OfflineUsername, OnlineUsername: OnlineUsername}); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	creds, err = SelectSandboxCredentials(home, SandboxNetworkIdentityOffline)
	if err != nil {
		t.Fatalf("SelectSandboxCredentials(missing users) error = %v", err)
	}
	if creds != nil {
		t.Fatalf("SelectSandboxCredentials(missing users) = %#v, want nil", creds)
	}
}

func TestDesiredOfflineProxySettingsPreserveUsesMarker(t *testing.T) {
	marker := &SetupMarker{
		Version:           SetupVersion,
		ProxyPorts:        []uint16{7777},
		AllowLocalBinding: true,
	}
	env := map[string]string{"HTTP_PROXY": "http://127.0.0.1:8080"}
	got := DesiredOfflineProxySettings(marker, ProxySettingsPreserve, env, SandboxNetworkIdentityOffline)
	if len(got.ProxyPorts) != 1 || got.ProxyPorts[0] != 7777 || !got.AllowLocalBinding {
		t.Fatalf("DesiredOfflineProxySettings(preserve) = %#v", got)
	}
	reconciled := DesiredOfflineProxySettings(marker, ProxySettingsReconcile, env, SandboxNetworkIdentityOffline)
	if len(reconciled.ProxyPorts) != 1 || reconciled.ProxyPorts[0] != 8080 || reconciled.AllowLocalBinding {
		t.Fatalf("DesiredOfflineProxySettings(reconcile) = %#v", reconciled)
	}
}

func TestRequireLogonSandboxCredsRunsSetupAndRefreshWithOverrides(t *testing.T) {
	oldSetup := runElevatedSetupForCredentials
	oldRefresh := runSetupRefreshForCredentials
	oldSelect := selectSandboxCredentialsForCredentials
	defer func() {
		runElevatedSetupForCredentials = oldSetup
		runSetupRefreshForCredentials = oldRefresh
		selectSandboxCredentialsForCredentials = oldSelect
	}()

	var setupReq *SandboxSetupRequest
	var refreshReq *SandboxSetupRequest
	runElevatedSetupForCredentials = func(req *SandboxSetupRequest) error {
		copyReq := *req
		setupReq = &copyReq
		return nil
	}
	runSetupRefreshForCredentials = func(req *SandboxSetupRequest) error {
		copyReq := *req
		refreshReq = &copyReq
		return nil
	}
	selectSandboxCredentialsForCredentials = func(codexHome string, identity SandboxNetworkIdentity) (*SandboxCredentials, error) {
		if identity != SandboxNetworkIdentityOffline {
			t.Fatalf("identity = %s, want offline", identity)
		}
		return &SandboxCredentials{Username: OfflineUsername, Password: "pw", Domain: "."}, nil
	}

	home := t.TempDir()
	profile := coresandbox.ReadOnlyPermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{`C:\repo`})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	creds, err := RequireLogonSandboxCredsForPermissions(
		permissions,
		`C:\repo`,
		map[string]string{"HTTP_PROXY": "http://127.0.0.1:8080"},
		home,
		[]string{`C:\read`},
		true,
		true,
		[]string{`C:\write`},
		true,
		[]string{`C:\secret`},
		[]string{`C:\readonly`},
		true,
		ProxySettingsReconcile,
	)
	if err != nil {
		t.Fatalf("RequireLogonSandboxCredsForPermissions() error = %v", err)
	}
	if creds == nil || creds.Username != OfflineUsername {
		t.Fatalf("creds = %#v", creds)
	}
	for label, req := range map[string]*SandboxSetupRequest{"setup": setupReq, "refresh": refreshReq} {
		if req == nil {
			t.Fatalf("%s request was not called", label)
		}
		if !req.Overrides.ReadRootsSet || len(req.Overrides.ReadRoots) != 1 || req.Overrides.ReadRoots[0] != `C:\read` {
			t.Fatalf("%s read overrides = %#v set=%v", label, req.Overrides.ReadRoots, req.Overrides.ReadRootsSet)
		}
		if !req.Overrides.WriteRootsSet || len(req.Overrides.WriteRoots) != 1 || req.Overrides.WriteRoots[0] != `C:\write` {
			t.Fatalf("%s write overrides = %#v set=%v", label, req.Overrides.WriteRoots, req.Overrides.WriteRootsSet)
		}
		if len(req.Overrides.DenyReadPaths) != 1 || len(req.Overrides.DenyWritePaths) != 1 {
			t.Fatalf("%s deny overrides = read %#v write %#v", label, req.Overrides.DenyReadPaths, req.Overrides.DenyWritePaths)
		}
		if req.OfflineProxySettings == nil || len(req.OfflineProxySettings.ProxyPorts) != 1 || req.OfflineProxySettings.ProxyPorts[0] != 8080 {
			t.Fatalf("%s proxy settings = %#v", label, req.OfflineProxySettings)
		}
	}
}

func TestRequireLogonSandboxCredsDisallowsImplicitElevation(t *testing.T) {
	oldSetup := runElevatedSetupForCredentials
	oldRefresh := runSetupRefreshForCredentials
	oldSelect := selectSandboxCredentialsForCredentials
	defer func() {
		runElevatedSetupForCredentials = oldSetup
		runSetupRefreshForCredentials = oldRefresh
		selectSandboxCredentialsForCredentials = oldSelect
	}()

	setupCalled := false
	runElevatedSetupForCredentials = func(*SandboxSetupRequest) error {
		setupCalled = true
		return nil
	}
	refreshCalled := false
	runSetupRefreshForCredentials = func(*SandboxSetupRequest) error {
		refreshCalled = true
		return nil
	}
	selectSandboxCredentialsForCredentials = func(string, SandboxNetworkIdentity) (*SandboxCredentials, error) {
		return nil, nil
	}

	home := t.TempDir()
	profile := coresandbox.ReadOnlyPermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{`C:\repo`})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	_, err = requireLogonSandboxCredsForPermissions(
		permissions,
		`C:\repo`,
		map[string]string{},
		home,
		nil,
		false,
		false,
		nil,
		false,
		nil,
		nil,
		false,
		ProxySettingsReconcile,
		false,
	)
	if !errors.Is(err, ErrSetupElevationDisallowed) {
		t.Fatalf("error = %v, want ErrSetupElevationDisallowed", err)
	}
	if setupCalled || refreshCalled {
		t.Fatalf("implicit setup/refresh called = %t/%t", setupCalled, refreshCalled)
	}
}
