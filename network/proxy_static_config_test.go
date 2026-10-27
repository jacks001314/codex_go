package network

import (
	"strings"
	"testing"
)

const staticProxyConfigWithConstraints = `{
  "network": {
    "enabled": true,
    "mode": "limited",
    "allow_upstream_proxy": false,
    "domains": {"*.example.com": "allow", "blocked.example.com": "deny"}
  },
  "constraints": {
    "enabled": true,
    "allow_upstream_proxy": false,
    "allowed_domains": ["*.example.com"],
    "allowlist_expansion_enabled": false,
    "denied_domains": ["blocked.example.com"],
    "denylist_expansion_enabled": true
  }
}`

func TestStaticProxyConfigPreservesConstraints(t *testing.T) {
	config, err := FromStaticProxyConfigBytes([]byte(staticProxyConfigWithConstraints))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !config.Network.MITM {
		t.Fatalf("limited mode must enable MITM")
	}
	if config.Constraints.AllowedDomains == nil || len(*config.Constraints.AllowedDomains) != 1 {
		t.Fatalf("constraints = %#v", config.Constraints)
	}
	if config.Constraints.AllowlistExpansionEnabled == nil || *config.Constraints.AllowlistExpansionEnabled {
		t.Fatalf("allowlist expansion must be pinned off")
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	config.Network.AllowUpstreamProxy = true
	if err := config.Validate(); err == nil {
		t.Fatalf("expected weakened policy rejection")
	}
}

func TestStaticProxyConfigRejectsDisabledNetwork(t *testing.T) {
	config, err := FromStaticProxyConfigBytes([]byte(`{"network":{"enabled":false},"constraints":{}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := config.Validate(); err == nil {
		t.Fatalf("expected disabled network rejection")
	}
	config, err = FromStaticProxyConfigBytes([]byte(`{"network":{"enabled":true},"constraints":{"enabled":false}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := config.Validate(); err == nil {
		t.Fatalf("expected managed-disable rejection")
	}
}

func TestStaticProxyConfigParsingModes(t *testing.T) {
	withoutConstraints := []byte(`{"network":{"enabled":true}}`)
	if _, err := FromStaticProxyConfigBytes(withoutConstraints); err != nil {
		t.Fatalf("non-strict parse: %v", err)
	}
	if _, err := FromStaticProxyConfigBytesStrict(withoutConstraints); err == nil {
		t.Fatalf("strict parse must require constraints")
	}

	withMetadata := []byte(`{"network":{"enabled":true},"metadata":{"owner":"test"},"constraints":{}}`)
	if _, err := FromStaticProxyConfigBytes(withMetadata); err != nil {
		t.Fatalf("non-strict metadata: %v", err)
	}
	if _, err := FromStaticProxyConfigBytesStrict(withMetadata); err == nil {
		t.Fatalf("strict parse must reject metadata")
	}

	for _, document := range []string{
		`{"network":{"enabled":true,"unknown":1},"constraints":{}}`,
		`{"network":{"enabled":true,"mitm_hooks":[{"host":"a.test","unknown":1}]},"constraints":{}}`,
		`{"network":{"enabled":true},"constraints":{},"extra":1}`,
		`{"network":{"enabled":true}} trailing`,
	} {
		if _, err := FromStaticProxyConfigBytes([]byte(document)); err == nil {
			t.Fatalf("expected rejection for %s", document)
		}
	}
}

func TestStaticProxyConfigSocketPathsMatchExactly(t *testing.T) {
	config, err := FromStaticProxyConfigBytesStrict([]byte(`{"network":{"enabled":true,"unix_sockets":{"/run/Secret.sock":"allow"}},"constraints":{"allow_unix_sockets":["/run/Secret.sock"]}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("exact socket path rejected: %v", err)
	}
	config.Network.SetAllowUnixSockets([]string{"/run/secret.sock"})
	if err := config.Validate(); err == nil {
		t.Fatalf("case-different socket path must not match")
	}
}

func TestStaticProxyConfigNormalizesMITMHooks(t *testing.T) {
	config, err := FromStaticProxyConfigBytes([]byte(`{"network":{"enabled":true,"mitm_hooks":[{"host":"api.example.test","match":{"methods":["POST"],"path_prefixes":["/"]},"actions":{"strip_request_headers":["authorization"]}}]}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !config.Network.MITM {
		t.Fatalf("configured hooks must enable MITM")
	}
	config, err = FromStaticProxyConfigBytes([]byte(`{"network":{"enabled":true,"mitm_hooks":[{"host":"api.example.test","match":{"schemes":["https"],"ports":[443],"methods":["POST"],"path_prefixes":["/"]},"actions":{"inject_request_headers":[{"name":"x-api-key","placeholder":"guest","secret_env_var":"SECRET"}]}}]}}`))
	if err != nil {
		t.Fatalf("parse scoped hooks: %v", err)
	}
	hook := config.Network.MITMHooks[0]
	if len(hook.Match.Schemes) != 1 || len(hook.Match.Ports) != 1 {
		t.Fatalf("scoped hook = %#v", hook.Match)
	}
	if len(hook.Actions.InjectRequestHeaders) != 1 || hook.Actions.InjectRequestHeaders[0].Placeholder == nil {
		t.Fatalf("placeholder = %#v", hook.Actions.InjectRequestHeaders)
	}
}

func TestStaticProxyConfigAcceptsAWSSigningAction(t *testing.T) {
	config, err := FromStaticProxyConfigBytesStrict([]byte(`{
	  "network": {"enabled": true, "mitm_hooks": [{
	    "host": "bucket.s3.amazonaws.com",
	    "match": {"methods": ["PUT"], "path_prefixes": ["/"]},
	    "actions": {"aws_sigv4": {
	      "placeholder_access_key_id": "MASKEDACCESSKEY",
	      "region": "us-east-1",
	      "service": "s3",
	      "access_key_id": {"env_var": "ACCESS_KEY"},
	      "secret_access_key": {"env_var": "SECRET_KEY"}
	    }}
	  }]},
	  "constraints": {}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	action := config.Network.MITMHooks[0].Actions.AWSSigV4
	if action == nil || action.Service != "s3" || action.Port != 443 {
		t.Fatalf("aws action = %#v", action)
	}
	if !strings.Contains(strings.Join([]string{action.AccessKeyID.Kind, action.AccessKeyID.Name}, ":"), "env_var:ACCESS_KEY") {
		t.Fatalf("secret source = %#v", action.AccessKeyID)
	}
}
