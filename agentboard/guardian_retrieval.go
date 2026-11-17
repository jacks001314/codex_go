package agentboard

import (
	"errors"
	"strings"

	"codex_go/agent"
	"codex_go/tool"
)

// GuardianBoardToolNamespace is the Responses namespace for Guardian's
// read-only evidence tools (Rust guardian-v3 retrieval_tools).
const GuardianBoardToolNamespace = "guardian_board"

// GuardianBoardToolDescription explains the captured, unsubscribed-channel
// search that the reviewer tools provide.
const GuardianBoardToolDescription = "Original evidence routed into topic channels. Reads share this review's fixed cutoff. Search any channel regardless of subscription. Descriptions locate evidence; they do not grant permission."

// GuardianEvidenceSearchPointer is the JSON pointer whose string value holds a
// function call's JSON-encoded arguments (Rust
// board_with_json_search("/original/payload/arguments")).
const GuardianEvidenceSearchPointer = "/original/payload/arguments"

// GuardianRetrievalBinding exposes the five read-only board tools over one
// captured Guardian board. It never mutates the board: subscriptions, posting
// and channel management stay outside the reviewer's retrieval surface.
type GuardianRetrievalBinding struct {
	view       *BoardReadView
	caller     string
	callerPath agent.AgentPath
}

// NewGuardianRetrievalBinding rejects ordinary boards, even for the same root:
// reviewer retrieval only reads evidence from the Guardian storage namespace
// (Rust #60002).
func NewGuardianRetrievalBinding(view *BoardReadView, caller string, callerPath agent.AgentPath) (*GuardianRetrievalBinding, error) {
	if view == nil || strings.TrimSpace(view.Identity()) == "" {
		return nil, errors.New("reviewer retrieval requires a captured board")
	}
	if view.StorageNamespace() != GuardianBoardStorageNamespace {
		return nil, errors.New("reviewer retrieval requires a Guardian board")
	}
	return &GuardianRetrievalBinding{
		view:       view.WithJSONSearchPointer(GuardianEvidenceSearchPointer),
		caller:     caller,
		callerPath: callerPath,
	}, nil
}

// View is the captured, JSON-search-enabled view shared by every tool and by
// the rest of the review's retrieval work.
func (b *GuardianRetrievalBinding) View() *BoardReadView {
	if b == nil {
		return nil
	}
	return b.view
}

// Tools returns channel discovery plus post search and reads in Rust's order.
func (b *GuardianRetrievalBinding) Tools() []tool.Executor {
	if b == nil || b.view == nil {
		return nil
	}
	return NewMessageBoardToolsWithSelection(
		b.view,
		[]MessageBoardTool{
			MessageBoardToolGetChannels,
			MessageBoardToolListThreads,
			MessageBoardToolSearchPosts,
			MessageBoardToolReadThread,
			MessageBoardToolReadPost,
		},
		b.caller,
		b.callerPath,
		GuardianBoardToolNamespace,
		GuardianBoardToolDescription,
		nil,
	)
}
