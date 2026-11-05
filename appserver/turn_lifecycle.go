package appserver

import (
	"strings"

	"codex_go/session"
)

// TurnItemCompletedInput is the host context delivered when a turn item
// completes. The stores have session, thread and turn lifetime respectively,
// matching the extension-data scopes Rust exposes to lifecycle contributors.
type TurnItemCompletedInput struct {
	ThreadID     string
	TurnID       string
	SessionStore *ExtensionStore
	ThreadStore  *ExtensionStore
	TurnStore    *ExtensionStore
	Item         ThreadItem
}

// TurnLifecycleContributor observes completed turn items. Contributors that
// need the host-supplied user-input data implement
// TurnLifecycleMetadataContributor as well; otherwise this compatibility
// callback is used, matching Rust's on_item_completed default.
type TurnLifecycleContributor interface {
	OnItemCompleted(TurnItemCompletedInput)
}

// TurnLifecycleMetadataContributor receives the per-input extension data
// alongside the same completion. Rust #59455 keeps this data host-only and
// never includes it in model input or persisted history.
type TurnLifecycleMetadataContributor interface {
	TurnLifecycleContributor
	OnItemCompletedWithMetadata(TurnItemCompletedInput, map[string]any)
}

// RegisterTurnLifecycleContributor installs a host lifecycle contributor for
// this router. Registration order is preserved.
func (r *RuntimeRouter) RegisterTurnLifecycleContributor(contributor TurnLifecycleContributor) {
	if r == nil || contributor == nil {
		return
	}
	r.turnLifecycleMu.Lock()
	defer r.turnLifecycleMu.Unlock()
	r.turnLifecycleContributors = append(r.turnLifecycleContributors, contributor)
}

func (r *RuntimeRouter) turnLifecycleContributorsSnapshot() []TurnLifecycleContributor {
	if r == nil {
		return nil
	}
	r.turnLifecycleMu.RLock()
	defer r.turnLifecycleMu.RUnlock()
	return append([]TurnLifecycleContributor(nil), r.turnLifecycleContributors...)
}

func (r *RuntimeRouter) emitTurnItemCompletedLifecycle(threadID string, turnID string, item ThreadItem, userInputExtensionData map[string]any) {
	if r == nil {
		return
	}
	contributors := r.turnLifecycleContributorsSnapshot()
	if len(contributors) == 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	input := TurnItemCompletedInput{
		ThreadID:    threadID,
		TurnID:      turnID,
		ThreadStore: r.internalHost.threadStore(threadID),
		TurnStore:   r.internalHost.turnStore(turnID),
		Item:        item,
	}
	if record, err := r.threadRecord(session.ThreadID(threadID), true, false); err == nil && record != nil {
		input.SessionStore = r.internalHost.sessionStore(firstNonEmpty(strings.TrimSpace(record.SessionID), threadID))
	}
	data := cloneAnyMap(userInputExtensionData)
	for _, contributor := range contributors {
		if metadataContributor, ok := contributor.(TurnLifecycleMetadataContributor); ok {
			metadataContributor.OnItemCompletedWithMetadata(input, cloneAnyMap(data))
			continue
		}
		contributor.OnItemCompleted(input)
	}
}
