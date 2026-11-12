package config

import (
	"strings"
	"testing"
)

// Mirrors Rust #61028: otel.headers_helper and its refresh cadence resolve into
// the effective OTel settings and are validated like the typed TOML struct.
func TestResolveOtelHeadersHelperConfigLikeRust(t *testing.T) {
	resolved, warnings := ResolveOtelConfig(map[string]any{"otel": map[string]any{
		"headers_helper":                     "print-headers",
		"headers_helper_refresh_interval_ms": int64(60_000),
	}})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if resolved.HeadersHelper != "print-headers" || resolved.HeadersHelperRefreshIntervalMS != 60_000 {
		t.Fatalf("resolved headers helper = %q / %d", resolved.HeadersHelper, resolved.HeadersHelperRefreshIntervalMS)
	}
	if defaults := DefaultOtelConfig(); defaults.HeadersHelper != "" || defaults.HeadersHelperRefreshIntervalMS != 0 {
		t.Fatalf("default headers helper = %q / %d, want unset", defaults.HeadersHelper, defaults.HeadersHelperRefreshIntervalMS)
	}
}

func TestValidateOtelHeadersHelperConfigLikeRust(t *testing.T) {
	cases := map[string]map[string]any{
		"non-string command":  {"headers_helper": 7},
		"empty command":       {"headers_helper": "   "},
		"zero interval":       {"headers_helper_refresh_interval_ms": int64(0)},
		"negative interval":   {"headers_helper_refresh_interval_ms": int64(-5)},
		"fractional interval": {"headers_helper_refresh_interval_ms": 1.5},
	}
	for name, otel := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateOtelConfigValues(map[string]any{"otel": otel})
			if err == nil {
				t.Fatalf("ValidateOtelConfigValues(%v) = nil, want an error", otel)
			}
			if !strings.Contains(err.Error(), "headers_helper") {
				t.Fatalf("error = %v, want it to name the field", err)
			}
		})
	}
	if err := ValidateOtelConfigValues(map[string]any{"otel": map[string]any{
		"headers_helper":                     "print-headers",
		"headers_helper_refresh_interval_ms": int64(29 * 60 * 1000),
	}}); err != nil {
		t.Fatalf("valid helper config error = %v", err)
	}
}
