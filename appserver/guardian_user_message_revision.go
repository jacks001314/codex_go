package appserver

import (
	"strings"

	"codex_go/session"
)

// recordGuardianUserMessageRevision advances the thread's host-owned
// user-message revision for every genuine user message in the batch
// (Rust #41660). Host-injected context is excluded by its content
// classification, so a goal update or environment fragment never invalidates
// Guardian authorization.
func (r *RuntimeRouter) recordGuardianUserMessageRevision(threadID string, items []session.Item) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || len(items) == 0 {
		return
	}
	delta := uint64(0)
	for index := range items {
		if sessionItemAdvancesUserAuthorizationRevision(&items[index]) {
			delta++
		}
	}
	if delta == 0 {
		return
	}
	r.bumpGuardianUserMessageRevisionBy(threadID, delta)
}

// bumpGuardianUserMessageRevision advances the revision by one. History resets
// and rollbacks call this even when they remove no user turn, mirroring Rust's
// `replace_annotated`, which always advances the revision.
func (r *RuntimeRouter) bumpGuardianUserMessageRevision(threadID string) {
	r.bumpGuardianUserMessageRevisionBy(threadID, 1)
}

func (r *RuntimeRouter) bumpGuardianUserMessageRevisionBy(threadID string, delta uint64) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || delta == 0 {
		return
	}
	r.guardianUserMessageRevisionMu.Lock()
	defer r.guardianUserMessageRevisionMu.Unlock()
	if r.guardianUserMessageRevisions == nil {
		r.guardianUserMessageRevisions = map[string]uint64{}
	}
	r.guardianUserMessageRevisions[threadID] += delta
}

// guardianUserMessageRevision returns the thread's current revision. A thread
// the runtime has not observed yet reports zero, which is the same value Rust's
// `ContextManager::new` starts at.
func (r *RuntimeRouter) guardianUserMessageRevision(threadID string) uint64 {
	if r == nil {
		return 0
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return 0
	}
	r.guardianUserMessageRevisionMu.Lock()
	defer r.guardianUserMessageRevisionMu.Unlock()
	return r.guardianUserMessageRevisions[threadID]
}

func (r *RuntimeRouter) forgetGuardianUserMessageRevision(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.guardianUserMessageRevisionMu.Lock()
	delete(r.guardianUserMessageRevisions, threadID)
	r.guardianUserMessageRevisionMu.Unlock()
}

// sessionItemAdvancesUserAuthorizationRevision applies Rust's
// `is_user_authorization_message` predicate to a persisted session item. An
// item without a persisted response body is legacy evidence, so it counts
// conservatively as user authorization.
func sessionItemAdvancesUserAuthorizationRevision(item *session.Item) bool {
	if item == nil {
		return false
	}
	fields, ok := sessionItemResponseItemFieldsFromItem(item)
	if ok {
		if role := strings.TrimSpace(fields.Role); role != "" && !strings.EqualFold(role, "user") {
			return false
		}
		return sessionItemIsUserAuthorizationMessage(fields, ok)
	}
	if !sessionItemIsUserMessage(item) {
		return false
	}
	return sessionItemIsUserAuthorizationMessage(fields, ok)
}
