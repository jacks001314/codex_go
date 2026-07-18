package tui

import (
	"path/filepath"

	"codex_go/appserver"
	"codex_go/config"
)

// Rust parity: codex-rs/tui/src/hooks_rpc.rs.

type HookRPCEvent struct {
	HookID string
	Phase  string
}

type HookTrustUpdate struct {
	Key         string
	CurrentHash string
}

func HooksListEntryForCWD(response appserver.HookListResponse, cwd string) appserver.HookListEntry {
	cleanCWD := filepath.Clean(cwd)
	for _, entry := range response.Data {
		if filepath.Clean(entry.CWD) == cleanCWD {
			return entry
		}
	}
	return appserver.HookListEntry{
		CWD:      cwd,
		Hooks:    []appserver.HookMetadata{},
		Warnings: []string{},
		Errors:   []appserver.HookErrorInfo{},
	}
}

func HookNeedsReview(hook appserver.HookMetadata) bool {
	return hook.TrustStatus == appserver.HookTrustUntrusted || hook.TrustStatus == appserver.HookTrustModified
}

func BuildHookTrustValue(trustUpdates []HookTrustUpdate) map[string]any {
	value := make(map[string]any, len(trustUpdates))
	for _, update := range trustUpdates {
		value[update.Key] = map[string]any{
			"trusted_hash": update.CurrentHash,
		}
	}
	return value
}

func BuildHookTrustWriteParams(trustUpdates []HookTrustUpdate) config.ConfigBatchWriteParams {
	return config.ConfigBatchWriteParams{
		Edits: []config.ConfigEdit{{
			KeyPath:       "hooks.state",
			Value:         BuildHookTrustValue(trustUpdates),
			MergeStrategy: config.MergeUpsert,
		}},
		ReloadUserConfig: true,
	}
}

func BuildSingleHookTrustWriteParams(key string, currentHash string) config.ConfigBatchWriteParams {
	return BuildHookTrustWriteParams([]HookTrustUpdate{{Key: key, CurrentHash: currentHash}})
}
