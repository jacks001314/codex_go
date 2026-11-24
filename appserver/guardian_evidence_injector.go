package appserver

// Rust parity: codex-rs/ext/guardian-v3/src/conversation.rs
// `Conversation::deliver` injects each admitted evidence batch into the retained
// reviewer thread with `CodexThread::inject_response_items`. Go's retained
// threads live behind thread/inject_items, so the injector routes the batch
// through the same runtime handler the app server uses for agent mail and side
// chat boundaries.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/agentboard"
)

// GuardianThreadEvidenceInjector records admitted Guardian evidence in one
// retained thread without starting a model turn.
type GuardianThreadEvidenceInjector struct {
	router   *RuntimeRouter
	threadID string
}

// NewGuardianThreadEvidenceInjector binds a retained reviewer thread to the
// runtime router that owns its history.
func NewGuardianThreadEvidenceInjector(router *RuntimeRouter, threadID string) (*GuardianThreadEvidenceInjector, error) {
	if router == nil {
		return nil, errors.New("Guardian evidence injection requires the runtime router")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("Guardian evidence injection requires a retained thread")
	}
	return &GuardianThreadEvidenceInjector{router: router, threadID: threadID}, nil
}

// ThreadID identifies the retained reviewer thread this injector records into.
func (i *GuardianThreadEvidenceInjector) ThreadID() string {
	if i == nil {
		return ""
	}
	return i.threadID
}

// InjectResponseItems appends the batch to the retained thread's history. An
// empty batch is a no-op, matching an inject with no items. The error is
// returned as-is so the reviewer conversation keeps its delivery cursor behind
// evidence that never reached the thread.
func (i *GuardianThreadEvidenceInjector) InjectResponseItems(ctx context.Context, items []json.RawMessage) error {
	if i == nil || i.router == nil {
		return errors.New("Guardian evidence injector is unavailable")
	}
	if len(items) == 0 {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	params, err := json.Marshal(ThreadInjectItemsParams{ThreadID: i.threadID, Items: items})
	if err != nil {
		return err
	}
	_, err = i.router.handleThreadInjectItemsRuntime(&Request{
		Method:   MethodThreadInjectItems,
		Params:   params,
		Internal: true,
	})
	return err
}

// guardianEvidenceInjector is the compile-time check that the thread injector
// satisfies the reviewer conversation's admission hook.
var _ agentboard.GuardianEvidenceInjector = (*GuardianThreadEvidenceInjector)(nil)
