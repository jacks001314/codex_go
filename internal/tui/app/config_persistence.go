package app

import (
	"strings"

	"codex_go/internal/config"
	"codex_go/internal/features"
	"codex_go/internal/sandbox"
)

type ConfigPersistenceRequest struct {
	KeyPath string
	Value   any
}

func OverriddenWriteMessage(writeResponse *config.ConfigWriteResponse) string {
	if writeResponse != nil && writeResponse.OverriddenMetadata != nil {
		return writeResponse.OverriddenMetadata.Message
	}
	return "the effective config is overridden by a higher-priority layer"
}

func FeatureEnabledFromEffectiveConfig(effectiveConfig *config.ConfigReadResponse, featureKey string) bool {
	return features.Enabled(FeaturesFromEffectiveConfig(effectiveConfig), featureKey)
}

func FeaturesFromEffectiveConfig(effectiveConfig *config.ConfigReadResponse) map[string]bool {
	out := map[string]bool{}
	raw, ok := effectiveConfigValue(effectiveConfig, "features")
	if !ok {
		return out
	}
	switch typed := raw.(type) {
	case map[string]bool:
		for key, value := range typed {
			out[key] = value
		}
	case map[string]any:
		for key, value := range typed {
			if enabled, ok := value.(bool); ok {
				out[key] = enabled
			}
		}
	}
	return out
}

func ApprovalsReviewerFromEffectiveConfig(effectiveConfig *config.ConfigReadResponse) (config.ApprovalsReviewer, bool) {
	value, ok := effectiveConfigString(effectiveConfig, "approvals_reviewer")
	if !ok {
		return "", false
	}
	switch config.ApprovalsReviewer(value) {
	case config.ApprovalsReviewerUser, config.ApprovalsReviewerAutoReview, config.ApprovalsReviewerGuardianSubagent:
		return config.ApprovalsReviewer(value), true
	default:
		return "", false
	}
}

func ApprovalPolicyFromEffectiveConfig(effectiveConfig *config.ConfigReadResponse) (sandbox.AskForApproval, bool) {
	value, ok := effectiveConfigString(effectiveConfig, "approval_policy")
	if !ok {
		return "", false
	}
	switch sandbox.AskForApproval(value) {
	case sandbox.ApprovalUnlessTrusted, sandbox.ApprovalOnRequest, sandbox.ApprovalGranular, sandbox.ApprovalNever:
		return sandbox.AskForApproval(value), true
	default:
		return "", false
	}
}

func SandboxModeFromEffectiveConfig(effectiveConfig *config.ConfigReadResponse) (sandbox.SandboxMode, bool) {
	value, ok := effectiveConfigString(effectiveConfig, "sandbox_mode")
	if !ok {
		return "", false
	}
	mode, err := sandbox.ParseSandboxMode(value)
	if err != nil {
		return "", false
	}
	return mode, true
}

func MemoriesFromEffectiveConfig(effectiveConfig *config.ConfigReadResponse) (map[string]any, bool) {
	raw, ok := effectiveConfigValue(effectiveConfig, "memories")
	if !ok {
		return nil, false
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	return cloneConfigMap(values), true
}

func WindowsSandboxModeFromEffectiveConfig(effectiveConfig *config.ConfigReadResponse) (config.WindowsSandboxSetupMode, bool) {
	raw, ok := effectiveConfigValue(effectiveConfig, "windows")
	if !ok {
		return "", false
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	mode, ok := valueAsString(values["sandbox"])
	if !ok {
		return "", false
	}
	switch config.WindowsSandboxSetupMode(mode) {
	case config.WindowsSandboxSetupDisabled, config.WindowsSandboxSetupDefault, config.WindowsSandboxSetupElevated, config.WindowsSandboxSetupUnelevated:
		return config.WindowsSandboxSetupMode(mode), true
	default:
		return "", false
	}
}

func effectiveConfigString(effectiveConfig *config.ConfigReadResponse, key string) (string, bool) {
	raw, ok := effectiveConfigValue(effectiveConfig, key)
	if !ok {
		return "", false
	}
	return valueAsString(raw)
}

func effectiveConfigValue(effectiveConfig *config.ConfigReadResponse, key string) (any, bool) {
	if effectiveConfig == nil || effectiveConfig.Config == nil {
		return nil, false
	}
	value, ok := effectiveConfig.Config[key]
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

func valueAsString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	if text == "" || strings.TrimSpace(text) != text {
		return "", false
	}
	return text, true
}

func cloneConfigMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
