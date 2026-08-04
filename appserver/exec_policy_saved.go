package appserver

// Rust parity: codex-rs/core/src/context/approved_command_prefix_saved.rs and
// the world-state diff in permissions.rs (Rust 1bbfb5cfad). After an
// exec-policy amendment is approved, the newly saved command prefix is reported
// to the model exactly once as "Approved command prefix saved:" instead of
// re-injecting the full permissions instructions.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

const approvedCommandPrefixSavedMessagePrefix = "Approved command prefix saved:"

type execPolicySavedFragment struct {
	prefix []string
}

type execPolicySavedState struct {
	mu    sync.Mutex
	saved map[string][]execPolicySavedFragment
}

func newExecPolicySavedState() *execPolicySavedState {
	return &execPolicySavedState{saved: map[string][]execPolicySavedFragment{}}
}

func execPolicySavedKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func (s *execPolicySavedState) remember(threadID string, turnID string, prefix []string) {
	if s == nil || len(prefix) == 0 {
		return
	}
	key := execPolicySavedKey(threadID, turnID)
	if key == "\x00" {
		return
	}
	cloned := append([]string(nil), prefix...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved[key] = append(s.saved[key], execPolicySavedFragment{prefix: cloned})
}

func (s *execPolicySavedState) take(threadID string, turnID string) []execPolicySavedFragment {
	if s == nil {
		return nil
	}
	key := execPolicySavedKey(threadID, turnID)
	s.mu.Lock()
	defer s.mu.Unlock()
	fragments := append([]execPolicySavedFragment(nil), s.saved[key]...)
	delete(s.saved, key)
	return fragments
}

func execPolicySavedText(fragment execPolicySavedFragment) string {
	if len(fragment.prefix) == 0 {
		return ""
	}
	prefixes := sandbox.FormatAllowPrefixes([][]string{fragment.prefix})
	if strings.TrimSpace(prefixes) == "" {
		return ""
	}
	return approvedCommandPrefixSavedMessagePrefix + "\n" + prefixes
}

func (r *RuntimeRouter) rememberExecPolicyAmendmentSaved(threadID string, turnID string, prefix []string) {
	if r == nil || r.execPolicySaved == nil || len(prefix) == 0 {
		return
	}
	r.execPolicySaved.remember(threadID, turnID, prefix)
}

// execPolicyPostToolInputItems drains amendments saved during tool execution
// and reports each newly approved command prefix to the model exactly once,
// mirroring Rust's ApprovedCommandPrefixSaved world-state diff.
func (r *RuntimeRouter) execPolicyPostToolInputItems(threadID string, turnID string, base turn.ToolPostExecutionInputItems, appendSessionItems func([]session.Item)) turn.ToolPostExecutionInputItems {
	if r == nil || r.execPolicySaved == nil {
		return base
	}
	return func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any {
		items := []any{}
		if base != nil {
			items = append(items, base(ctx, invocation, output)...)
		}
		fragments := r.execPolicySaved.take(threadID, turnID)
		if len(fragments) == 0 {
			return items
		}
		createdAt := time.Now().UTC()
		if output != nil && !output.CompletedAt.IsZero() {
			createdAt = output.CompletedAt.UTC()
		}
		sessionItems := make([]session.Item, 0, len(fragments))
		for index, fragment := range fragments {
			text := execPolicySavedText(fragment)
			if text == "" {
				continue
			}
			items = append(items, modelInputTextMessage("developer", text))
			sessionItems = append(sessionItems, session.Item{
				ID:        fmt.Sprintf("exec-policy-saved-%s-%d", safeIdentifier(turnID), index+1),
				Type:      "message",
				Role:      "developer",
				Text:      text,
				CreatedAt: createdAt,
				Metadata:  appTurnMetadata(turnID, map[string]any{"kind": "approved_command_prefix_saved"}),
			})
		}
		if appendSessionItems != nil {
			appendSessionItems(sessionItems)
		}
		return items
	}
}
