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

func TestBrowserComputerUseRequirementsFromMapExpanded(t *testing.T) {
	req := ConfigRequirementsFromMap(map[string]any{
		"allow_browser_and_computer_use": true,
		"browser_use": map[string]any{
			"allow_history_access":          true,
			"disable_auto_review":           true,
			"allow_global_persistent_approval": true,
			"default_origin_policy": map[string]any{
				"access":            "allow",
				"full_cdp_access":   "deny",
			},
			"origins": map[string]any{
				"https://example.com": map[string]any{
					"auto_review": "allow",
				},
			},
		},
		"computer_use": map[string]any{
			"allow_locked_computer_use": true,
			"allow_persistent_approval": true,
			"default_app_access":        "deny",
			"windows": map[string]any{
				"exes": []any{
					map[string]any{
						"publisher_name": "Example Co",
						"product_name":   "Example App",
						"access":         "deny",
					},
				},
			},
		},
	})
	if req == nil {
		t.Fatal("expected requirements")
	}
	if req.AllowBrowserAndComputerUse == nil || !*req.AllowBrowserAndComputerUse {
		t.Fatalf("AllowBrowserAndComputerUse not parsed: %+v", req.AllowBrowserAndComputerUse)
	}
	if req.BrowserUse == nil || req.BrowserUse.AllowHistoryAccess == nil || !*req.BrowserUse.AllowHistoryAccess {
		t.Fatalf("browser_use allow_history_access not parsed: %+v", req.BrowserUse)
	}
	if req.BrowserUse.DefaultOriginPolicy == nil || req.BrowserUse.DefaultOriginPolicy.Access == nil {
		t.Fatalf("default_origin_policy not parsed: %+v", req.BrowserUse)
	}
	if len(req.BrowserUse.Origins) != 1 {
		t.Fatalf("origins not parsed: %+v", req.BrowserUse.Origins)
	}
	if req.ComputerUse == nil || req.ComputerUse.DefaultAppAccess == nil || *req.ComputerUse.DefaultAppAccess != AllowDenyRequirementDeny {
		t.Fatalf("computer_use default_app_access not parsed: %+v", req.ComputerUse)
	}
	if len(req.ComputerUse.Windows.Exes) != 1 || req.ComputerUse.Windows.Exes[0].PublisherName != "Example Co" {
		t.Fatalf("computer_use windows exes not parsed: %+v", req.ComputerUse.Windows)
	}
}
