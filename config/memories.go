package config

const (
	DefaultMemoriesMaxRolloutsPerStartup          = 2
	DefaultMemoriesMaxRolloutAgeDays              = int64(10)
	DefaultMemoriesMinRolloutIdleHours            = int64(6)
	DefaultMemoriesMinRateLimitRemainingPercent   = int64(25)
	DefaultMemoriesMaxRawMemoriesForConsolidation = 256
	DefaultMemoriesMaxUnusedDays                  = int64(30)
)

type MemoriesConfig struct {
	DisableOnExternalContext       bool
	GenerateMemories               bool
	UseMemories                    bool
	DedicatedTools                 bool
	MaxRawMemoriesForConsolidation int
	MaxUnusedDays                  int64
	MaxRolloutAgeDays              int64
	MaxRolloutsPerStartup          int
	MinRolloutIdleHours            int64
	MinRateLimitRemainingPercent   int64
	ExtractModel                   *string
	ConsolidationModel             *string
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
	}
}

func (c *Config) Memories() MemoriesConfig {
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
	result.DedicatedTools = memoryBool(values, "dedicated_tools", result.DedicatedTools)
	result.MaxRawMemoriesForConsolidation = int(clampMemoryInt(memoryInt(values, "max_raw_memories_for_consolidation", int64(result.MaxRawMemoriesForConsolidation)), 1, 4096))
	result.MaxUnusedDays = clampMemoryInt(memoryInt(values, "max_unused_days", result.MaxUnusedDays), 0, 365)
	result.MaxRolloutAgeDays = clampMemoryInt(memoryInt(values, "max_rollout_age_days", result.MaxRolloutAgeDays), 0, 90)
	result.MaxRolloutsPerStartup = int(clampMemoryInt(memoryInt(values, "max_rollouts_per_startup", int64(result.MaxRolloutsPerStartup)), 1, 128))
	result.MinRolloutIdleHours = clampMemoryInt(memoryInt(values, "min_rollout_idle_hours", result.MinRolloutIdleHours), 1, 48)
	result.MinRateLimitRemainingPercent = clampMemoryInt(memoryInt(values, "min_rate_limit_remaining_percent", result.MinRateLimitRemainingPercent), 0, 100)
	result.ExtractModel = memoryString(values, "extract_model")
	result.ConsolidationModel = memoryString(values, "consolidation_model")
	return result
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
