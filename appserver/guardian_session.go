package appserver

import "strings"

// guardianReviewerName mirrors Rust's
// codex_guardian_reviewer::session::GUARDIAN_REVIEWER_NAME.
const guardianReviewerName = "guardian"

// guardianPromptCacheKeyOverride mirrors Rust's
// codex_guardian_reviewer::prompt_cache_key_override_for_review_session. A
// Guardian reviewer scopes its Responses prompt cache to the reviewed parent
// thread, so reviewer conversations for that thread share a cache prefix
// without colliding with an ordinary session's cache key. Non-Guardian session
// sources and a missing parent thread produce no override.
func guardianPromptCacheKeyOverride(sessionSource, parentThreadID string) string {
	if strings.TrimSpace(sessionSource) != guardianReviewerName {
		return ""
	}
	parentThreadID = strings.TrimSpace(parentThreadID)
	if parentThreadID == "" {
		return ""
	}
	return guardianReviewerName + ":" + parentThreadID
}
