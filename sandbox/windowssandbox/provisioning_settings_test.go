package windowssandbox

import (
	"reflect"
	"testing"

	coresandbox "codex_go/sandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

func TestProvisioningSettingsFromEnvironmentSelectsOfflineIdentity(t *testing.T) {
	env := map[string]string{
		"HTTP_PROXY":                        "http://127.0.0.1:7897",
		"ALL_PROXY":                         "socks5://localhost:1080",
		"CODEX_NETWORK_ALLOW_LOCAL_BINDING": "1",
	}
	offline := ProvisioningSettingsFromEnvironment(&coresandbox.PermissionProfile{}, env)
	if !reflect.DeepEqual(offline.ProxyPorts, []uint16{1080, 7897}) {
		t.Fatalf("offline proxy ports = %v", offline.ProxyPorts)
	}
	if !offline.AllowLocalBinding {
		t.Fatal("offline settings must carry local binding")
	}
	online := ProvisioningSettingsFromEnvironment(&coresandbox.PermissionProfile{NetworkEnabled: true}, env)
	if len(online.ProxyPorts) != 0 || online.AllowLocalBinding {
		t.Fatalf("online settings = %#v", online)
	}
	disabled := ProvisioningSettingsFromEnvironment(&coresandbox.PermissionProfile{Disabled: true}, env)
	if len(disabled.ProxyPorts) != 0 {
		t.Fatalf("disabled profiles count as an enabled network sandbox: %#v", disabled)
	}
}

func TestProvisioningListenersClassifyLoopbackProxies(t *testing.T) {
	listeners := ProvisioningListenersFromProxyEnvironment(map[string]string{
		"HTTP_PROXY":  "http://127.0.0.1:7897",
		"HTTPS_PROXY": "https://localhost:7897/ignored",
		"ALL_PROXY":   "socks5h://127.0.0.1:1080",
		"WS_PROXY":    "socks4://[::1]:9050",
		"WSS_PROXY":   "http://example.com:8080",
	})
	if !reflect.DeepEqual(listeners.HTTPPorts, []uint16{7897}) {
		t.Fatalf("http ports = %v", listeners.HTTPPorts)
	}
	if !reflect.DeepEqual(listeners.SOCKSPorts, []uint16{1080, 9050}) {
		t.Fatalf("socks ports = %v", listeners.SOCKSPorts)
	}
}

func TestProvisioningListenersSkipEnabledNetworkSandbox(t *testing.T) {
	env := map[string]string{"HTTP_PROXY": "http://127.0.0.1:7897"}
	empty := EmptyProvisioningListeners()
	if got := ProvisioningListenersFromEnvironment(&coresandbox.PermissionProfile{NetworkEnabled: true}, env); !reflect.DeepEqual(got, empty) {
		t.Fatalf("enabled network listeners = %#v", got)
	}
	if got := ProvisioningListenersFromEnvironment(&coresandbox.PermissionProfile{}, env); !reflect.DeepEqual(got.HTTPPorts, []uint16{7897}) {
		t.Fatalf("offline listeners = %#v", got)
	}
}

func TestFilterProvisioningListenersKeepsSavedPortsLikeRust(t *testing.T) {
	listeners := provisionservice.Listeners{
		HTTPPorts:  []uint16{7897, 8080},
		SOCKSPorts: []uint16{1080, 9050},
	}
	filtered := FilterProvisioningListeners(listeners, []uint16{7897, 9050})
	if !reflect.DeepEqual(filtered.HTTPPorts, []uint16{7897}) {
		t.Fatalf("http ports = %v", filtered.HTTPPorts)
	}
	if !reflect.DeepEqual(filtered.SOCKSPorts, []uint16{9050}) {
		t.Fatalf("socks ports = %v", filtered.SOCKSPorts)
	}
}
