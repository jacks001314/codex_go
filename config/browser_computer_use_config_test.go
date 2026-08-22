package config

import (
	"strings"
	"testing"
)

func TestValidateBrowserComputerUseConfigValuesAcceptsValidRustShape(t *testing.T) {
	valid := map[string]any{
		"browser_use": map[string]any{
			"allow_history_access": true,
			"default_origin_policy": map[string]any{
				"access": "allow",
			},
			"origins": map[string]any{
				"https://example.com": map[string]any{
					"downloads":          "deny",
					"uploads":            "allow",
					"full_cdp_access":    "deny",
					"persistent_approval": true,
				},
			},
		},
		"computer_use": map[string]any{
			"default_app_access": "allow",
			"macos": map[string]any{
				"bundle_ids": map[string]any{
					"com.example.app": "deny",
				},
			},
			"windows": map[string]any{
				"aumids": map[string]any{
					"Microsoft.WindowsTerminal": "allow",
				},
				"exes": []any{
					map[string]any{
						"publisher_name": "Example Co",
						"product_name":   "Example App",
						"binary_name":    "example.exe",
						"access":         "allow",
					},
				},
			},
		},
	}
	if err := validateBrowserComputerUseConfigValues(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateBrowserComputerUseConfigValuesRejectsUnknownFields(t *testing.T) {
	invalid := map[string]any{
		"browser_use": map[string]any{
			"not_a_real_field": "x",
		},
	}
	err := validateBrowserComputerUseConfigValues(invalid)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}

	invalidComputer := map[string]any{
		"computer_use": map[string]any{
			"default_app_access": "maybe",
		},
	}
	err = validateBrowserComputerUseConfigValues(invalidComputer)
	if err == nil || !strings.Contains(err.Error(), "must be allow or deny") {
		t.Fatalf("expected allow/deny error, got %v", err)
	}
}
