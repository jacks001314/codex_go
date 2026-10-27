package tui

import (
	"fmt"
	"path/filepath"
	"strings"

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

// HooksListTarget mirrors Rust hooks_rpc::HooksListTarget: a running thread's
// current hook inventory, or a working directory inspected before a thread
// exists (Rust #56553).
type HooksListTarget struct {
	ThreadID string
	CWD      string
}

func HooksListTargetForThread(threadID string) HooksListTarget {
	return HooksListTarget{ThreadID: strings.TrimSpace(threadID)}
}

func HooksListTargetForCWD(cwd string) HooksListTarget {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		cwd = filepath.Clean(cwd)
	}
	return HooksListTarget{CWD: cwd}
}

func (t HooksListTarget) IsThread() bool {
	return strings.TrimSpace(t.ThreadID) != ""
}

func (t HooksListTarget) String() string {
	if t.IsThread() {
		return fmt.Sprintf("Thread(%q)", t.ThreadID)
	}
	return fmt.Sprintf("Cwd(%q)", t.CWD)
}

// BuildHooksListParams mirrors Rust fetch_hooks_list's request construction:
// thread mode asks for a loaded thread's inventory, cwd mode inspects one
// directory.
func BuildHooksListParams(target HooksListTarget) appserver.HookListParams {
	if target.IsThread() {
		threadID := strings.TrimSpace(target.ThreadID)
		return appserver.HookListParams{ThreadID: &threadID}
	}
	params := appserver.HookListParams{}
	if cwd := strings.TrimSpace(target.CWD); cwd != "" {
		params.CWDs = []string{cwd}
	}
	return params
}

// HooksListEntryFromResponse mirrors Rust fetch_hooks_list: the first inventory
// is the requested one, and an empty response is an error.
func HooksListEntryFromResponse(response appserver.HookListResponse, target HooksListTarget) (appserver.HookListEntry, error) {
	if len(response.Data) == 0 {
		return appserver.HookListEntry{}, fmt.Errorf("hooks/list returned no inventory for %s", target)
	}
	return response.Data[0], nil
}

// HooksListRequestError mirrors Rust's user-visible hooks/list failure surface:
// fetch_hooks_list wraps the typed client error, whose Display already names
// the method and JSON-RPC code, e.g.
// "hooks/list failed in TUI: hooks/list failed: thread not found: x (code -32600)".
func HooksListRequestError(message string, code int) error {
	return fmt.Errorf("hooks/list failed in TUI: hooks/list failed: %s (code %d)", strings.TrimSpace(message), code)
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
