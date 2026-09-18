package plugin

import (
	"fmt"
	"strings"
	"unicode"
)

// PluginIdError represents a validation or parsing error for PluginId.
type PluginIdError struct {
	Message string
}

func (e *PluginIdError) Error() string {
	return e.Message
}

func newPluginIdError(msg string) *PluginIdError {
	return &PluginIdError{Message: msg}
}

// PluginId is a validated identifier for a plugin, consisting of a plugin name and marketplace name.
// When serialized as a key, it uses the format "plugin_name@marketplace_name".
type PluginId struct {
	PluginName      string
	MarketplaceName string
}

// NewPluginId creates a validated PluginId. Both segments must be non-empty and
// contain only ASCII alphanumeric characters, '_', and '-'; the plugin name may
// additionally use '.' to separate non-empty name segments.
func NewPluginId(pluginName string, marketplaceName string) (*PluginId, error) {
	if err := validatePluginSegment(pluginName, "plugin name"); err != nil {
		return nil, err
	}
	if err := validatePluginSegment(marketplaceName, "marketplace name"); err != nil {
		return nil, err
	}
	return &PluginId{
		PluginName:      pluginName,
		MarketplaceName: marketplaceName,
	}, nil
}

// ParsePluginId parses a plugin key string in the format "<plugin>@<marketplace>".
// Uses the last '@' as the delimiter, matching Rust's rsplit_once behavior. The
// input is not trimmed, matching Rust's PluginId::parse.
func ParsePluginId(pluginKey string) (*PluginId, error) {
	idx := strings.LastIndex(pluginKey, "@")
	if idx < 0 {
		return nil, newPluginIdError(fmt.Sprintf("invalid plugin key %q; expected <plugin>@<marketplace>", pluginKey))
	}
	pluginName := pluginKey[:idx]
	marketplaceName := pluginKey[idx+1:]
	if pluginName == "" || marketplaceName == "" {
		return nil, newPluginIdError(fmt.Sprintf("invalid plugin key %q; expected <plugin>@<marketplace>", pluginKey))
	}

	id, err := NewPluginId(pluginName, marketplaceName)
	if err != nil {
		return nil, newPluginIdError(fmt.Sprintf("%s in %q", err.Error(), pluginKey))
	}
	return id, nil
}

// Key returns the canonical string representation "plugin_name@marketplace_name".
func (id *PluginId) Key() string {
	if id == nil {
		return ""
	}
	return id.PluginName + "@" + id.MarketplaceName
}

// String returns the canonical key representation.
func (id *PluginId) String() string {
	return id.Key()
}

// Clone returns a deep copy of the PluginId.
func (id *PluginId) Clone() *PluginId {
	if id == nil {
		return nil
	}
	return &PluginId{
		PluginName:      id.PluginName,
		MarketplaceName: id.MarketplaceName,
	}
}

// ValidatePluginSegment validates a single segment used in plugin IDs and cache
// layout. Segments must be non-empty and contain only ASCII alphanumeric, '_',
// and '-' characters; a "plugin name" may additionally use '.' as a separator
// between non-empty name segments (Rust validate_plugin_segment).
func ValidatePluginSegment(segment string, kind string) error {
	return validatePluginSegment(segment, kind)
}

func validatePluginSegment(segment string, kind string) error {
	if segment == "" {
		return newPluginIdError(fmt.Sprintf("invalid %s: must not be empty", kind))
	}
	allowDots := kind == "plugin name"
	if allowDots {
		if segment == "." || segment == ".." {
			return newPluginIdError(fmt.Sprintf("invalid %s: path traversal is not allowed", kind))
		}
		if strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".") || strings.Contains(segment, "..") {
			return newPluginIdError(fmt.Sprintf("invalid %s: dots must separate non-empty name segments", kind))
		}
	}
	allowedCharacters := pluginSegmentAllowedCharacters(allowDots)
	for _, ch := range segment {
		if ch > unicode.MaxASCII {
			return newPluginIdError(fmt.Sprintf("invalid %s: only %s are allowed", kind, allowedCharacters))
		}
		if !isPluginSegmentChar(byte(ch)) && !(allowDots && ch == '.') {
			return newPluginIdError(fmt.Sprintf("invalid %s: only %s are allowed", kind, allowedCharacters))
		}
	}
	return nil
}

func pluginSegmentAllowedCharacters(allowDots bool) string {
	if allowDots {
		return "ASCII letters, digits, '.', '_', and '-'"
	}
	return "ASCII letters, digits, '_', and '-'"
}

// IsValidRemotePluginID mirrors
// codex_core_plugins::remote::is_valid_remote_plugin_id: a remote plugin id is
// non-empty and contains only ASCII letters, digits, '_', '-', and '~'. The
// value is not trimmed, matching Rust.
func IsValidRemotePluginID(pluginID string) bool {
	if pluginID == "" {
		return false
	}
	for _, ch := range pluginID {
		if ch > unicode.MaxASCII {
			return false
		}
		if !isPluginSegmentChar(byte(ch)) && ch != '~' {
			return false
		}
	}
	return true
}

func isPluginSegmentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_'
}
