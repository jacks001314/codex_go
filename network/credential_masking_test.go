package network

import "testing"

// #52302: brokerage requires an enabled proxy (Rust
// core/src/config/network_proxy_spec.rs:96-98 adds the `self.enabled()`
// conjunct, and :381-390 forces the broker off before the config state is
// built). A disabled proxy must therefore build an inert broker even when the
// settings still carry credential_broker = true.
func TestProxyRuntimePolicyKeepsBrokerInertWhenProxyDisabledLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.MITM = true
	settings.CredentialBroker = true
	settings.Enabled = false
	if CredentialBrokerEnabledForSettings(settings) {
		t.Fatal("CredentialBrokerEnabledForSettings must require an enabled proxy")
	}
	policy, err := buildProxyRuntimePolicy(settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy.broker.Enabled() {
		t.Fatal("a disabled proxy must not activate the credential broker")
	}
	settings.Enabled = true
	if !CredentialBrokerEnabledForSettings(settings) {
		t.Fatal("an enabled proxy with credential_broker must activate brokerage")
	}
	policy, err = buildProxyRuntimePolicy(settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.broker.Enabled() {
		t.Fatal("an enabled proxy with credential_broker must build an active broker")
	}
	settings.CredentialBroker = false
	if CredentialBrokerEnabledForSettings(settings) {
		t.Fatal("credential_broker = false must keep the broker inert")
	}
}
