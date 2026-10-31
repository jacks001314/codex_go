package windowssandbox

import (
	"errors"
	"reflect"
	"testing"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

func stubProvisioningService(t *testing.T, provision func(codexHome string, settings provisionservice.Settings, listeners provisionservice.Listeners) (provisionservice.Outcome, error)) *bool {
	t.Helper()
	originalProvision := provisionViaServiceForCredentials
	originalFallback := runElevatedSetupFallbackForCredentials
	fallbackCalled := false
	provisionViaServiceForCredentials = provision
	runElevatedSetupFallbackForCredentials = func(*SandboxSetupRequest) error {
		fallbackCalled = true
		return nil
	}
	t.Cleanup(func() {
		provisionViaServiceForCredentials = originalProvision
		runElevatedSetupFallbackForCredentials = originalFallback
	})
	return &fallbackCalled
}

func TestAutomaticSetupPrefersServiceAndFiltersSavedProxyPorts(t *testing.T) {
	var gotSettings provisionservice.Settings
	var gotListeners provisionservice.Listeners
	fallbackCalled := stubProvisioningService(t, func(codexHome string, settings provisionservice.Settings, listeners provisionservice.Listeners) (provisionservice.Outcome, error) {
		if codexHome != `C:\home` {
			t.Fatalf("codexHome = %q", codexHome)
		}
		gotSettings, gotListeners = settings, listeners
		return provisionservice.Provisioned, nil
	})
	request := &SandboxSetupRequest{
		CodexHome: `C:\home`,
		Env: map[string]string{
			"HTTP_PROXY": "http://127.0.0.1:7897",
			"ALL_PROXY":  "socks5://127.0.0.1:1080",
		},
		OfflineProxySettings: &OfflineProxySettings{ProxyPorts: []uint16{7897}},
	}
	if err := runAutomaticSetupForCredentials(request); err != nil {
		t.Fatalf("runAutomaticSetupForCredentials() error = %v", err)
	}
	if *fallbackCalled {
		t.Fatal("elevated helper must not run after service provisioning")
	}
	if !reflect.DeepEqual(gotSettings.ProxyPorts, []uint16{7897}) || gotSettings.AllowLocalBinding {
		t.Fatalf("settings = %#v", gotSettings)
	}
	if !reflect.DeepEqual(gotListeners.HTTPPorts, []uint16{7897}) {
		t.Fatalf("http listeners = %v", gotListeners.HTTPPorts)
	}
	if len(gotListeners.SOCKSPorts) != 0 {
		t.Fatalf("socks listeners must be filtered to saved ports: %v", gotListeners.SOCKSPorts)
	}
}

func TestAutomaticSetupFallsBackOnlyWhenServiceUnavailable(t *testing.T) {
	fallbackCalled := stubProvisioningService(t, func(string, provisionservice.Settings, provisionservice.Listeners) (provisionservice.Outcome, error) {
		return provisionservice.Unavailable, nil
	})
	request := &SandboxSetupRequest{CodexHome: `C:\home`, Env: map[string]string{}}
	if err := runAutomaticSetupForCredentials(request); err != nil {
		t.Fatalf("runAutomaticSetupForCredentials() error = %v", err)
	}
	if !*fallbackCalled {
		t.Fatal("unavailable service must fall back to the elevated helper")
	}
}

func TestAutomaticSetupPropagatesServiceErrors(t *testing.T) {
	fallbackCalled := stubProvisioningService(t, func(string, provisionservice.Settings, provisionservice.Listeners) (provisionservice.Outcome, error) {
		return provisionservice.Unavailable, errors.New("refused")
	})
	request := &SandboxSetupRequest{CodexHome: `C:\home`, Env: map[string]string{}}
	if err := runAutomaticSetupForCredentials(request); err == nil || err.Error() != "refused" {
		t.Fatalf("error = %v", err)
	}
	if *fallbackCalled {
		t.Fatal("service errors must not fall back")
	}
}

func TestAutomaticSetupRejectsNilRequest(t *testing.T) {
	if err := runAutomaticSetupForCredentials(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v", err)
	}
}
