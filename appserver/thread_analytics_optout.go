package appserver

import (
	"strings"

	"codex_go/session"
)

// threadAnalyticsDisabled reports whether thread-scoped analytics events must
// be suppressed for a thread (Rust #44646). A host-level disabled client
// suppresses everything; a thread or turn configuration may opt out without
// affecting sibling threads, and cannot re-enable a disabled host client.
func (r *RuntimeRouter) threadAnalyticsDisabled(threadID string) bool {
	if r == nil || r.services.Analytics == nil {
		return true
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	if r.threads != nil {
		if active := r.threads.ActiveTurn(threadID); active != nil && active.Params != nil {
			if analyticsDisabledByConfig(active.Params.Config) {
				return true
			}
		}
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return false
	}
	return analyticsDisabledByConfig(threadRecordConfigOverrides(record))
}

func analyticsDisabledByConfig(values map[string]any) bool {
	if len(values) == 0 {
		return false
	}
	for _, key := range []string{"analytics_enabled", "analyticsEnabled"} {
		value, ok := values[key]
		if !ok {
			continue
		}
		if enabled, ok := value.(bool); ok && !enabled {
			return true
		}
	}
	return false
}
