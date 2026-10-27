package network

import (
	"strings"
	"testing"
)

func awsSigV4TestConfigValues(action map[string]any) map[string]any {
	return map[string]any{
		"default_permissions": "aws",
		"permissions": map[string]any{
			"aws": map[string]any{
				"network": map[string]any{
					"mitm": map[string]any{
						"version": 2,
						"actions": map[string]any{"sign": map[string]any{"aws_sigv4": action}},
						"hooks": map[string]any{
							"aws": map[string]any{
								"host":          "bucket.s3.amazonaws.com",
								"methods":       []any{"PUT"},
								"path_prefixes": []any{"/"},
								"action":        []any{"sign"},
							},
						},
					},
				},
			},
		},
	}
}

func awsSigV4TestMITM(values map[string]any) map[string]any {
	return values["permissions"].(map[string]any)["aws"].(map[string]any)["network"].(map[string]any)["mitm"].(map[string]any)
}

func awsSigV4TestAction() map[string]any {
	return map[string]any{
		"placeholder_access_key_id": "MASKEDACCESSKEY",
		"region":                    "us-east-1",
		"service":                   "s3",
		"access_key_id":             map[string]any{"env_var": "ACCESS_KEY"},
		"secret_access_key":         map[string]any{"env_var": "SECRET_KEY"},
	}
}

func TestProxyConfigParsesAWSSigV4Action(t *testing.T) {
	config, err := ProxyConfigFromConfigValues(awsSigV4TestConfigValues(awsSigV4TestAction()))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(config.Network.MITMHooks) != 1 {
		t.Fatalf("hooks = %#v", config.Network.MITMHooks)
	}
	action := config.Network.MITMHooks[0].Actions.AWSSigV4
	if action == nil {
		t.Fatalf("missing aws_sigv4 action")
	}
	if action.PlaceholderAccessKeyID != "MASKEDACCESSKEY" || action.Region != "us-east-1" || action.Service != "s3" {
		t.Fatalf("unexpected action %#v", action)
	}
	if action.Port != 443 || action.MaxBodyBytes != DefaultAwsSigV4MaxBodyBytes {
		t.Fatalf("unexpected defaults %#v", action)
	}
	if action.AccessKeyID != (ProxySecretSource{Kind: "env_var", Name: "ACCESS_KEY"}) {
		t.Fatalf("unexpected secret source %#v", action.AccessKeyID)
	}
	if action.SessionToken != nil {
		t.Fatalf("unexpected session token %#v", action.SessionToken)
	}
}

func TestProxyConfigParsesInlineAWSSigV4Action(t *testing.T) {
	hook, err := proxyMITMHookConfigFromTable(map[string]any{
		"host":  "bucket.s3.amazonaws.com",
		"match": map[string]any{"methods": []any{"PUT"}, "path_prefixes": []any{"/"}},
		"actions": map[string]any{"aws_sigv4": map[string]any{
			"placeholder_access_key_id": "MASKEDACCESSKEY",
			"region":                    "us-east-1",
			"service":                   "s3",
			"access_key_id":             map[string]any{"env_var": "ACCESS_KEY"},
			"secret_access_key":         map[string]any{"file": `C:\run\secrets\aws`},
			"session_token":             map[string]any{"env_var": "SESSION_TOKEN"},
			"port":                      int64(8443),
			"max_body_bytes":            int64(1024),
		}},
	})
	if err != nil {
		t.Fatalf("parse hook: %v", err)
	}
	action := hook.Actions.AWSSigV4
	if action == nil {
		t.Fatalf("missing aws_sigv4 action")
	}
	if action.Port != 8443 || action.MaxBodyBytes != 1024 {
		t.Fatalf("unexpected numeric settings %#v", action)
	}
	if action.SecretAccessKey != (ProxySecretSource{Kind: "file", Name: `C:\run\secrets\aws`}) {
		t.Fatalf("unexpected secret source %#v", action.SecretAccessKey)
	}
	if action.SessionToken == nil || action.SessionToken.Kind != "env_var" {
		t.Fatalf("unexpected session token %#v", action.SessionToken)
	}
}

func TestProxyConfigRejectsMultipleAWSSigningActions(t *testing.T) {
	values := awsSigV4TestConfigValues(awsSigV4TestAction())
	mitm := awsSigV4TestMITM(values)
	mitm["actions"].(map[string]any)["sign_again"] = map[string]any{"aws_sigv4": awsSigV4TestAction()}
	hook := mitm["hooks"].(map[string]any)["aws"].(map[string]any)
	hook["action"] = []any{"sign", "sign_again"}
	_, err := ProxyConfigFromConfigValues(values)
	if err == nil || !strings.Contains(err.Error(), "must select at most one AWS signing action") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProxyConfigRejectsInvalidAWSSigV4Credentials(t *testing.T) {
	values := awsSigV4TestConfigValues(awsSigV4TestAction())
	mitm := awsSigV4TestMITM(values)
	action := mitm["actions"].(map[string]any)["sign"].(map[string]any)["aws_sigv4"].(map[string]any)
	action["access_key_id"] = map[string]any{"env_var": "A=B"}
	if _, err := ProxyConfigFromConfigValues(values); err == nil {
		t.Fatalf("expected invalid env_var rejection")
	}
	action["access_key_id"] = map[string]any{"env_var": "ACCESS_KEY", "file": `C:\run\secret`}
	if _, err := ProxyConfigFromConfigValues(values); err == nil {
		t.Fatalf("expected ambiguous secret source rejection")
	}
	action["access_key_id"] = map[string]any{"file": "relative/path"}
	if _, err := ProxyConfigFromConfigValues(values); err == nil {
		t.Fatalf("expected relative file rejection")
	}
	action["access_key_id"] = map[string]any{"env_var": "ACCESS_KEY"}
	action["unknown"] = "value"
	if _, err := ProxyConfigFromConfigValues(values); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field rejection: %v", err)
	}
}

func TestCompileProxyMITMHooksRejectsOverlappingPlaceholders(t *testing.T) {
	values := awsSigV4TestConfigValues(awsSigV4TestAction())
	config, err := ProxyConfigFromConfigValues(values)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	env := map[string]string{"ACCESS_KEY": "AKIDEXAMPLE", "SECRET_KEY": "wJalrXUtnFEMI"}
	resolve := func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}
	if _, err := CompileProxyMITMHooksWithResolvers(*config, resolve, nil); err != nil {
		t.Fatalf("compile hooks: %v", err)
	}
	mitm := awsSigV4TestMITM(values)
	action := mitm["actions"].(map[string]any)["sign"].(map[string]any)["aws_sigv4"].(map[string]any)
	action["placeholder_access_key_id"] = "GUESTAKIDEXAMPLE"
	config, err = ProxyConfigFromConfigValues(values)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if _, err := CompileProxyMITMHooksWithResolvers(*config, resolve, nil); err == nil {
		t.Fatalf("expected overlapping placeholder rejection")
	}
}
