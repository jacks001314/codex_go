package guardiancontext

// ConversationTranscriptKind is the semantic role of one parent-conversation
// transcript entry.
type ConversationTranscriptKind int

const (
	// ConversationTranscriptUser is an actual parent-thread user message.
	ConversationTranscriptUser ConversationTranscriptKind = iota
	// ConversationTranscriptDeveloper is an explicit developer message
	// preserving a user-approved action.
	ConversationTranscriptDeveloper
	// ConversationTranscriptAssistant is assistant commentary or an
	// inter-agent message.
	ConversationTranscriptAssistant
	// ConversationTranscriptProtectedAssistant is a final assistant answer
	// eligible for stronger asynchronous retention.
	ConversationTranscriptProtectedAssistant
	// ConversationTranscriptToolCall is a named tool invocation, including
	// shell and web-search calls.
	ConversationTranscriptToolCall
	// ConversationTranscriptToolOutput is a named or unnamed tool result.
	ConversationTranscriptToolOutput
	// ConversationTranscriptNodeReplToolOutput is a result from a Node
	// REPL-backed tool that may receive a larger synchronous cap.
	ConversationTranscriptNodeReplToolOutput
	// ConversationTranscriptReasoning is plaintext model reasoning that the
	// asynchronous consumer explicitly enables.
	ConversationTranscriptReasoning
)

// ConversationTranscriptEntryKind mirrors Rust's
// ConversationTranscriptEntryKind: it preserves the semantic role and, for the
// tool variants, the named tool used in Guardian transcript prompts.
type ConversationTranscriptEntryKind struct {
	// Kind selects the role, retention pool, and per-entry limits.
	Kind ConversationTranscriptKind
	// Tool is the descriptive tool role carried by the tool variants. The
	// message and reasoning variants ignore it.
	Tool string
}

// UserKind returns the entry kind for an actual parent-thread user message.
func UserKind() ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptUser}
}

// DeveloperKind returns the entry kind for an explicit developer message that
// preserves a user-approved action.
func DeveloperKind() ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptDeveloper}
}

// AssistantKind returns the entry kind for assistant commentary or an
// inter-agent message.
func AssistantKind() ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptAssistant}
}

// ProtectedAssistantKind returns the entry kind for a final assistant answer
// eligible for stronger asynchronous retention.
func ProtectedAssistantKind() ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptProtectedAssistant}
}

// ToolCallKind returns the entry kind for a named tool invocation.
func ToolCallKind(tool string) ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptToolCall, Tool: tool}
}

// ToolOutputKind returns the entry kind for a named or unnamed tool result.
func ToolOutputKind(tool string) ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptToolOutput, Tool: tool}
}

// NodeReplToolOutputKind returns the entry kind for a result produced by a Node
// REPL-backed tool.
func NodeReplToolOutputKind(tool string) ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptNodeReplToolOutput, Tool: tool}
}

// ReasoningKind returns the entry kind for plaintext model reasoning.
func ReasoningKind() ConversationTranscriptEntryKind {
	return ConversationTranscriptEntryKind{Kind: ConversationTranscriptReasoning}
}

// Role returns the role label currently used in Guardian transcript prompts.
func (k ConversationTranscriptEntryKind) Role() string {
	switch k.Kind {
	case ConversationTranscriptUser:
		return "user"
	case ConversationTranscriptDeveloper:
		return "developer"
	case ConversationTranscriptAssistant, ConversationTranscriptProtectedAssistant:
		return "assistant"
	case ConversationTranscriptToolCall, ConversationTranscriptToolOutput, ConversationTranscriptNodeReplToolOutput:
		return k.Tool
	case ConversationTranscriptReasoning:
		return "reasoning"
	default:
		return "assistant"
	}
}

// ConversationTranscriptEntry is structured text evidence shared by synchronous
// Guardian review and asynchronous scoring.
type ConversationTranscriptEntry struct {
	// Kind preserves source attribution for consumer-specific retention and
	// rendering.
	Kind ConversationTranscriptEntryKind
	// Text is bounded by the current request's per-entry limits.
	Text string
	// OriginalBytes is the size before truncation, retained for omission and
	// truncation accounting.
	OriginalBytes int
}
