package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultMemoriesMaxRolloutsPerStartup          = 2
	DefaultMemoriesMaxRolloutAgeDays              = int64(10)
	DefaultMemoriesMinRolloutIdleHours            = int64(6)
	DefaultMemoriesMinRateLimitRemainingPercent   = int64(25)
	DefaultMemoriesMaxRawMemoriesForConsolidation = 256
	DefaultMemoriesMaxUnusedDays                  = int64(30)
	// DefaultMemoriesConsolidationServiceTier keeps background consolidation on
	// the standard tier (Rust #57568: SERVICE_TIER_DEFAULT_REQUEST_VALUE).
	DefaultMemoriesConsolidationServiceTier = "default"
)

// MemoryVersion selects the memory namespace (Rust MemoryVersion, #43797). An
// explicit `memories.version` wins; fresh profiles default to V2 during config
// load when memories are enabled (Rust #56211).
type MemoryVersion string

const (
	MemoryVersionV1 MemoryVersion = "v1"
	MemoryVersionV2 MemoryVersion = "v2"
)

// ParseMemoryVersion resolves a configured `memories.version` value. An empty
// value selects v1 (the default); anything other than v1/v2 is invalid.
func ParseMemoryVersion(value string) (MemoryVersion, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return MemoryVersionV1, true
	case string(MemoryVersionV1):
		return MemoryVersionV1, true
	case string(MemoryVersionV2):
		return MemoryVersionV2, true
	default:
		return "", false
	}
}

// DirectoryName is the sibling root name for the version (Rust
// MemoryVersion::directory_name).
func (v MemoryVersion) DirectoryName() string {
	if v == MemoryVersionV2 {
		return "memories_v2"
	}
	return "memories"
}

type MemoriesConfig struct {
	DisableOnExternalContext bool
	GenerateMemories         bool
	UseMemories              bool
	// DualWrite runs v1 and v2 extraction/consolidation concurrently with
	// isolated stores so v2 can warm in the background (Rust #43827).
	DualWrite bool
	// DualWriteConfigured records that `memories.dual_write` was set explicitly,
	// so an explicit false can opt a fresh profile out of the V2 default.
	DualWriteConfigured bool
	DedicatedTools      bool
	// Version is the raw configured `memories.version` value. Use
	// MemoryVersion() to resolve it (empty selects v1).
	Version                        *string
	MaxRawMemoriesForConsolidation int
	MaxUnusedDays                  int64
	MaxRolloutAgeDays              int64
	MaxRolloutsPerStartup          int
	MinRolloutIdleHours            int64
	MinRateLimitRemainingPercent   int64
	ExtractModel                   *string
	ConsolidationModel             *string
	// ConsolidationServiceTier is the service tier for background memory
	// consolidation, independent of the session's tier (Rust #57568).
	ConsolidationServiceTier string
}

func DefaultMemoriesConfig() MemoriesConfig {
	return MemoriesConfig{
		GenerateMemories:               true,
		UseMemories:                    true,
		MaxRawMemoriesForConsolidation: DefaultMemoriesMaxRawMemoriesForConsolidation,
		MaxUnusedDays:                  DefaultMemoriesMaxUnusedDays,
		MaxRolloutAgeDays:              DefaultMemoriesMaxRolloutAgeDays,
		MaxRolloutsPerStartup:          DefaultMemoriesMaxRolloutsPerStartup,
		MinRolloutIdleHours:            DefaultMemoriesMinRolloutIdleHours,
		MinRateLimitRemainingPercent:   DefaultMemoriesMinRateLimitRemainingPercent,
		ConsolidationServiceTier:       DefaultMemoriesConsolidationServiceTier,
	}
}

func (c *Config) Memories() MemoriesConfig {
	result := c.memoriesFromValues()
	if result.Version == nil && c != nil && c.resolvedMemoryVersion == MemoryVersionV2 {
		version := string(MemoryVersionV2)
		result.Version = &version
	}
	return result
}

// memoriesFromValues reads the raw `[memories]` table without applying the
// fresh-profile default, so config load can resolve that default itself.
func (c *Config) memoriesFromValues() MemoriesConfig {
	result := DefaultMemoriesConfig()
	if c == nil || c.Values == nil {
		return result
	}
	values, ok := c.Values["memories"].(map[string]any)
	if !ok {
		return result
	}
	result.DisableOnExternalContext = memoryBool(values, "disable_on_external_context", memoryBool(values, "no_memories_if_mcp_or_web_search", result.DisableOnExternalContext))
	result.GenerateMemories = memoryBool(values, "generate_memories", result.GenerateMemories)
	result.UseMemories = memoryBool(values, "use_memories", result.UseMemories)
	result.DualWrite = memoryBool(values, "dual_write", result.DualWrite)
	_, result.DualWriteConfigured = values["dual_write"].(bool)
	result.DedicatedTools = memoryBool(values, "dedicated_tools", result.DedicatedTools)
	result.Version = memoryString(values, "version")
	result.MaxRawMemoriesForConsolidation = int(clampMemoryInt(memoryInt(values, "max_raw_memories_for_consolidation", int64(result.MaxRawMemoriesForConsolidation)), 1, 4096))
	result.MaxUnusedDays = clampMemoryInt(memoryInt(values, "max_unused_days", result.MaxUnusedDays), 0, 365)
	result.MaxRolloutAgeDays = clampMemoryInt(memoryInt(values, "max_rollout_age_days", result.MaxRolloutAgeDays), 0, 90)
	result.MaxRolloutsPerStartup = int(clampMemoryInt(memoryInt(values, "max_rollouts_per_startup", int64(result.MaxRolloutsPerStartup)), 1, 128))
	result.MinRolloutIdleHours = clampMemoryInt(memoryInt(values, "min_rollout_idle_hours", result.MinRolloutIdleHours), 1, 48)
	result.MinRateLimitRemainingPercent = clampMemoryInt(memoryInt(values, "min_rate_limit_remaining_percent", result.MinRateLimitRemainingPercent), 0, 100)
	result.ExtractModel = memoryString(values, "extract_model")
	result.ConsolidationModel = memoryString(values, "consolidation_model")
	if tier := memoryString(values, "consolidation_service_tier"); tier != nil {
		result.ConsolidationServiceTier = NormalizeConsolidationServiceTier(*tier)
	}
	return result
}

// NormalizeConsolidationServiceTier applies the request aliases Rust accepts for
// service tiers (Rust #57568: `ServiceTier::from_request_value`). Unknown values
// are kept verbatim, like Rust's `map_or(tier, ...)`.
func NormalizeConsolidationServiceTier(value string) string {
	trimmed := strings.TrimSpace(value)
	switch trimmed {
	case "fast", "priority":
		return "priority"
	case "flex":
		return "flex"
	default:
		if trimmed == "" {
			return DefaultMemoriesConsolidationServiceTier
		}
		return trimmed
	}
}

func memoryBool(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func memoryInt(values map[string]any, key string, fallback int64) int64 {
	switch value := values[key].(type) {
	case int:
		return int64(value)
	case int8:
		return int64(value)
	case int16:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case uint:
		if uint64(value) <= uint64(^uint64(0)>>1) {
			return int64(value)
		}
	case uint8:
		return int64(value)
	case uint16:
		return int64(value)
	case uint32:
		return int64(value)
	case uint64:
		if value <= uint64(^uint64(0)>>1) {
			return int64(value)
		}
	}
	return fallback
}

func memoryString(values map[string]any, key string) *string {
	value, ok := values[key].(string)
	if !ok {
		return nil
	}
	return &value
}

func clampMemoryInt(value, minimum, maximum int64) int64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

// MemoryVersion resolves the configured version, defaulting to v1. An
// unrecognized value is reported as v1; config load rejects it up front.
func (c MemoriesConfig) MemoryVersion() MemoryVersion {
	if c.Version == nil {
		return MemoryVersionV1
	}
	version, ok := ParseMemoryVersion(*c.Version)
	if !ok {
		return MemoryVersionV1
	}
	return version
}

// ResolveFreshMemoryVersion mirrors Rust #56211's fresh-profile default: when
// memories are enabled, `memories.version` is unset, dual-write was not
// explicitly disabled, and the V1 workspace does not exist, the V2 pipeline is
// selected. Explicit selections and filesystem errors keep V1.
func ResolveFreshMemoryVersion(codexHome string, memories MemoriesConfig, memoriesEnabled bool) MemoryVersion {
	if memories.Version != nil {
		// An explicit selection always wins, including an explicit v1.
		if version, ok := ParseMemoryVersion(*memories.Version); ok {
			return version
		}
		return MemoryVersionV1
	}
	if !memoriesEnabled || (memories.DualWriteConfigured && !memories.DualWrite) {
		return MemoryVersionV1
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return MemoryVersionV1
	}
	_, err := os.Stat(filepath.Join(codexHome, MemoryVersionV1.DirectoryName()))
	if err == nil {
		return MemoryVersionV1
	}
	if errors.Is(err, os.ErrNotExist) {
		return MemoryVersionV2
	}
	return MemoryVersionV1
}
