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

// NewPluginId creates a validated PluginId. Both segments must be non-empty and contain only
// ASCII alphanumeric characters, '_', and '-'.
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
// Uses the last '@' as the delimiter, matching Rust's rsplit_once behavior.
func ParsePluginId(pluginKey string) (*PluginId, error) {
	trimmed := strings.TrimSpace(pluginKey)
	if trimmed == "" {
		return nil, newPluginIdError(fmt.Sprintf("invalid plugin key %q: must not be empty", pluginKey))
	}
	idx := strings.LastIndex(trimmed, "@")
	if idx < 0 {
		return nil, newPluginIdError(fmt.Sprintf("invalid plugin key %q; expected <plugin>@<marketplace>", pluginKey))
	}
	pluginName := trimmed[:idx]
	marketplaceName := trimmed[idx+1:]
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

// ValidatePluginSegment validates a single segment used in plugin IDs and cache layout.
// Segments must be non-empty and contain only ASCII alphanumeric, '_', and '-' characters.
func ValidatePluginSegment(segment string, kind string) error {
	return validatePluginSegment(segment, kind)
}

func validatePluginSegment(segment string, kind string) error {
	if segment == "" {
		return newPluginIdError(fmt.Sprintf("invalid %s: must not be empty", kind))
	}
	for _, ch := range segment {
		if ch > unicode.MaxASCII {
			return newPluginIdError(fmt.Sprintf("invalid %s: only ASCII letters, digits, '_', and '-' are allowed", kind))
		}
		if !isPluginSegmentChar(byte(ch)) {
			return newPluginIdError(fmt.Sprintf("invalid %s: only ASCII letters, digits, '_', and '-' are allowed", kind))
		}
	}
	return nil
}

func isPluginSegmentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_'
}
