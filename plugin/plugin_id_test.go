package plugin

import (
	"strings"
	"testing"
)

func TestNewPluginIdValidates(t *testing.T) {
	// Valid IDs
	tests := []struct {
		name        string
		pluginName  string
		marketplace string
	}{
		{"simple", "github", "openai-curated"},
		{"with-dashes", "google-calendar", "openai-curated"},
		{"with-underscores", "openai_developers", "openai_curated"},
		{"mixed_alphanumeric", "linear", "my-marketplace"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := NewPluginId(tc.pluginName, tc.marketplace)
			if err != nil {
				t.Fatalf("NewPluginId(%q, %q) error = %v", tc.pluginName, tc.marketplace, err)
			}
			if id.PluginName != tc.pluginName || id.MarketplaceName != tc.marketplace {
				t.Fatalf("NewPluginId fields mismatch: got %+v", id)
			}
		})
	}

	// Invalid IDs
	invalidTests := []struct {
		name        string
		pluginName  string
		marketplace string
		wantErr     string
	}{
		{"empty plugin name", "", "marketplace", "must not be empty"},
		{"empty marketplace", "plugin", "", "must not be empty"},
		{"space in name", "plugin name", "marketplace", "only ASCII"},
		{"non-ASCII name", "pluginé", "marketplace", "only ASCII"},
		{"slash in name", "plugin/name", "marketplace", "only ASCII"},
		{"dot in name", "plugin.name", "marketplace", "only ASCII"},
	}
	for _, tc := range invalidTests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPluginId(tc.pluginName, tc.marketplace)
			if err == nil {
				t.Fatalf("NewPluginId(%q, %q) expected error, got nil", tc.pluginName, tc.marketplace)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NewPluginId(%q, %q) error = %v, want containing %q", tc.pluginName, tc.marketplace, err, tc.wantErr)
			}
		})
	}
}

func TestParsePluginId(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantPlugin string
		wantMkt    string
		wantErr    bool
		errMsg     string
	}{
		{"simple", "github@openai-curated", "github", "openai-curated", false, ""},
		{"multiple at signs", "github@openai-curated@extra", "", "", true, "only ASCII"},
		{"empty", "", "", "", true, "must not be empty"},
		{"no at", "github", "", "", true, "expected <plugin>@<marketplace>"},
		{"empty name", "@openai-curated", "", "", true, "expected <plugin>@<marketplace>"},
		{"empty marketplace", "github@", "", "", true, "expected <plugin>@<marketplace>"},
		{"invalid name chars", "github.extra@marketplace", "", "", true, "only ASCII"},
		{"just at sign", "@", "", "", true, "expected <plugin>@<marketplace>"},
		{"spaces only", "  ", "", "", true, "must not be empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := ParsePluginId(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParsePluginId(%q) expected error, got nil", tc.input)
				}
				if tc.errMsg != "" && !strings.Contains(err.Error(), tc.errMsg) {
					t.Fatalf("ParsePluginId(%q) error = %v, want containing %q", tc.input, err, tc.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePluginId(%q) error = %v", tc.input, err)
			}
			if id.PluginName != tc.wantPlugin || id.MarketplaceName != tc.wantMkt {
				t.Fatalf("ParsePluginId(%q) = %+v, want %s@%s", tc.input, id, tc.wantPlugin, tc.wantMkt)
			}
		})
	}
}

func TestPluginIdKey(t *testing.T) {
	id, err := NewPluginId("github", "openai-curated")
	if err != nil {
		t.Fatalf("NewPluginId error = %v", err)
	}
	if key := id.Key(); key != "github@openai-curated" {
		t.Fatalf("Key() = %q, want %q", key, "github@openai-curated")
	}
	if s := id.String(); s != "github@openai-curated" {
		t.Fatalf("String() = %q, want %q", s, "github@openai-curated")
	}

	var nilID *PluginId
	if key := nilID.Key(); key != "" {
		t.Fatalf("nil Key() = %q, want empty", key)
	}
}

func TestPluginIdClone(t *testing.T) {
	id, _ := NewPluginId("github", "openai-curated")
	clone := id.Clone()

	if clone.PluginName != id.PluginName || clone.MarketplaceName != id.MarketplaceName {
		t.Fatalf("Clone() mismatch: %+v vs %+v", id, clone)
	}

	// Verify it's a deep copy (modifying clone doesn't affect original)
	clone.PluginName = "modified"
	if id.PluginName == clone.PluginName {
		t.Fatalf("Clone should be a deep copy")
	}

	var nilID *PluginId
	if nilClone := nilID.Clone(); nilClone != nil {
		t.Fatalf("nil Clone() should return nil, got %+v", nilClone)
	}
}

func TestValidatePluginSegment(t *testing.T) {
	tests := []struct {
		segment  string
		kind     string
		wantErr  bool
	}{
		{"valid-name", "test", false},
		{"Valid_Name_123", "test", false},
		{"x", "test", false},
		{"", "test", true},
		{"invalid space", "test", true},
		{"invalid/slash", "test", true},
		{"invalid.dot", "test", true},
		{"invalid@at", "test", true},
		{"non-ASCII-é", "test", true},
	}
	for _, tc := range tests {
		t.Run(tc.segment, func(t *testing.T) {
			err := ValidatePluginSegment(tc.segment, tc.kind)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidatePluginSegment(%q, %q) expected error, got nil", tc.segment, tc.kind)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidatePluginSegment(%q, %q) error = %v", tc.segment, tc.kind, err)
			}
		})
	}
}

func TestPluginIdThreadedIntoDiscovery(t *testing.T) {
	// Verify that PluginId integrates with the existing allowlist discovery
	// Test discovery of a known curated plugin
	for _, key := range ToolSuggestDiscoverablePluginAllowlist {
		id, err := ParsePluginId(key)
		if err != nil {
			t.Fatalf("ParsePluginId(%q) error for allowlist entry: %v", key, err)
		}
		if !IsToolSuggestFallbackPlugin(id) {
			t.Fatalf("IsToolSuggestFallbackPlugin(%q) should be true for curated entry", key)
		}
	}
}

func TestIsToolSuggestFallbackPluginCrossReference(t *testing.T) {
	// openai-api-curated should cross-reference to openai-curated
	id, _ := NewPluginId("github", "openai-api-curated")
	if !IsToolSuggestFallbackPlugin(id) {
		t.Fatalf("IsToolSuggestFallbackPlugin(github@openai-api-curated) should be true due to cross-reference")
	}

	// Unknown plugin should not be in allowlist
	unknown, _ := NewPluginId("unknown-plugin", "random-marketplace")
	if IsToolSuggestFallbackPlugin(unknown) {
		t.Fatalf("IsToolSuggestFallbackPlugin(unknown) should be false")
	}

	// nil should return false
	if IsToolSuggestFallbackPlugin(nil) {
		t.Fatalf("IsToolSuggestFallbackPlugin(nil) should be false")
	}
}

func TestCuratedPluginAllowlist(t *testing.T) {
	names := CuratedPluginAllowlist()
	if len(names) != 16 {
		// 14 unique names from openai-curated + 2 from openai-bundled
		t.Fatalf("CuratedPluginAllowlist() len = %d, want 16", len(names))
	}
	// Verify sorted
	for i := 1; i < len(names); i++ {
		if strings.ToLower(names[i]) < strings.ToLower(names[i-1]) {
			t.Fatalf("CuratedPluginAllowlist() not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
