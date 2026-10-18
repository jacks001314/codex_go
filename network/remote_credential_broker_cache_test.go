package network

import (
	"context"
	"strings"
	"testing"
)

func TestRemoteCredentialBrokerCacheReusesMappingsPerEnvironmentAndConversationLikeRust(t *testing.T) {
	cache := &RemoteCredentialBrokerCache{}
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.SetCredentialBrokerEnabled(true)

	first := cache.BrokerIfEnabled("executor-a", "conversation-a", settings, nil)
	second := cache.BrokerIfEnabled("executor-a", "conversation-a", settings, nil)
	if first == nil || first != second {
		t.Fatalf("broker reuse = %p/%p, want the same broker", first, second)
	}
	if otherConversation := cache.BrokerIfEnabled("executor-a", "conversation-b", settings, nil); otherConversation == first {
		t.Fatal("conversation changed but the broker was reused")
	}
	if otherEnvironment := cache.BrokerIfEnabled("executor-b", "conversation-a", settings, nil); otherEnvironment == first {
		t.Fatal("environment changed but the broker was reused")
	}
	if otherDestination := cache.BrokerIfEnabled("executor-a", "conversation-a", settings, map[string]string{"GH_HOST": "ghe.example"}); otherDestination == first {
		t.Fatal("destination context changed but the broker was reused")
	}
	disabled := settings
	disabled.Enabled = false
	if cache.BrokerIfEnabled("executor-a", "conversation-a", disabled, nil) != nil {
		t.Fatal("a disabled proxy created a broker")
	}
}

func TestCredentialMaskingFailureModesLikeRust(t *testing.T) {
	broker := NewProxyCredentialBroker(true)
	realKey := "sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	masked := map[string]string{"OPENAI_API_KEY": realKey, "SAFE": "value"}
	broker.VirtualizeChildEnv(masked)
	if err := broker.HandleUnmaskedCredentials(masked, CredentialMaskingFailureError); err != nil {
		t.Fatalf("fully masked environment rejected: %v", err)
	}

	raw := map[string]string{"OPENAI_API_KEY": realKey, "SAFE": "value"}
	if err := broker.HandleUnmaskedCredentials(raw, CredentialMaskingFailureError); err == nil || !strings.Contains(err.Error(), "unmasked credential") {
		t.Fatalf("error mode = %v", err)
	}
	if err := broker.HandleUnmaskedCredentials(raw, CredentialMaskingFailureDeny); err != nil {
		t.Fatalf("deny mode error = %v", err)
	}
	if _, ok := raw["OPENAI_API_KEY"]; ok || raw["SAFE"] != "value" {
		t.Fatalf("deny mode environment = %#v", raw)
	}
	raw = map[string]string{"OPENAI_API_KEY": realKey}
	if err := broker.HandleUnmaskedCredentials(raw, CredentialMaskingFailureWarn); err != nil {
		t.Fatalf("warn mode error = %v", err)
	}
	if raw["OPENAI_API_KEY"] != realKey {
		t.Fatal("warn mode removed the credential")
	}

	child := map[string]string{"OPENAI_API_KEY": realKey, "HTTP_PROXY": "http://127.0.0.1:1234"}
	broker.VirtualizeChildEnv(child)
	broker.RestoreAndDisableChildEnv(child)
	if child["OPENAI_API_KEY"] != realKey || child["HTTP_PROXY"] != "http://127.0.0.1:1234" {
		t.Fatalf("restored child = %#v", child)
	}
	if _, ok := child[CredentialBrokerActiveEnvKey]; ok {
		t.Fatal("restored child retained the broker marker")
	}
	next := map[string]string{"OPENAI_API_KEY": realKey}
	broker.VirtualizeChildEnv(next)
	if next["OPENAI_API_KEY"] == realKey {
		t.Fatal("restoring one child discarded mappings for later commands")
	}
}

func TestRemoteBrokerMappingSurvivesProxyLaunchReuseLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.SetCredentialBrokerEnabled(true)
	cache := &RemoteCredentialBrokerCache{}
	env := map[string]string{"OPENAI_API_KEY": "sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"}
	broker := cache.BrokerIfEnabled("executor-a", "conversation-a", settings, env)
	if broker == nil {
		t.Fatal("broker was not created")
	}
	config := ProxyConfig{Network: settings, CredentialBroker: broker}
	first, err := StartProxyManagedNetwork(context.Background(), config, env)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := StartProxyManagedNetwork(context.Background(), config, env)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstDummy := first.EnvSnapshot()["OPENAI_API_KEY"]
	secondDummy := second.EnvSnapshot()["OPENAI_API_KEY"]
	if firstDummy == "" || firstDummy == env["OPENAI_API_KEY"] || secondDummy != firstDummy {
		t.Fatalf("mapping reuse = %q/%q for real %q", firstDummy, secondDummy, env["OPENAI_API_KEY"])
	}
}
