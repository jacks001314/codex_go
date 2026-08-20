package app

import "strings"

// Rust parity subset: codex-rs/tui/src/app/app_server_requests.rs.

const (
	ServerRequestDynamicToolCall          = "dynamic_tool_call"
	ServerRequestChatGPTAuthTokensRefresh = "chatgpt_auth_tokens_refresh"
	ServerRequestAttestationGenerate      = "attestation_generate"
	ServerRequestCurrentTimeRead          = "current_time_read"
)

type ServerRequest struct {
	ID         string
	Kind       string
	ThreadID   string
	TurnID     string
	ItemID     string
	ApprovalID string
	ServerName string
}

type UnsupportedAppServerRequest struct {
	RequestID string
	Message   string
}

type ResolvedAppServerRequestKind string

const (
	ResolvedAppServerRequestExecApproval        ResolvedAppServerRequestKind = "exec_approval"
	ResolvedAppServerRequestFileChangeApproval  ResolvedAppServerRequestKind = "file_change_approval"
	ResolvedAppServerRequestPermissionsApproval ResolvedAppServerRequestKind = "permissions_approval"
	ResolvedAppServerRequestUserInput           ResolvedAppServerRequestKind = "user_input"
	ResolvedAppServerRequestMcpElicitation      ResolvedAppServerRequestKind = "mcp_elicitation"
)

type ResolvedAppServerRequest struct {
	Kind       ResolvedAppServerRequestKind
	ID         string
	ThreadID   string
	CallID     string
	ServerName string
	RequestID  string
}

type PendingAppServerRequests struct {
	execApprovals        map[pendingApprovalKey]string
	fileChangeApprovals  map[pendingApprovalKey]string
	permissionsApprovals map[pendingApprovalKey]string
	userInputs           map[string][]pendingUserInputRequest
	mcpRequests          map[mcpRequestKey]string
}

// pendingApprovalKey scopes pending app-server approvals by both the thread
// and the item id so colliding ids across concurrent threads never resolve,
// submit, or dismiss the wrong request (Rust #39372).
type pendingApprovalKey struct {
	threadID string
	itemID   string
}

type pendingUserInputRequest struct {
	ItemID    string
	RequestID string
}

type mcpRequestKey struct {
	ServerName string
	RequestID  string
}

func NewPendingAppServerRequests() *PendingAppServerRequests {
	p := &PendingAppServerRequests{}
	p.ensure()
	return p
}

func (p *PendingAppServerRequests) Clear() {
	if p == nil {
		return
	}
	p.execApprovals = map[pendingApprovalKey]string{}
	p.fileChangeApprovals = map[pendingApprovalKey]string{}
	p.permissionsApprovals = map[pendingApprovalKey]string{}
	p.userInputs = map[string][]pendingUserInputRequest{}
	p.mcpRequests = map[mcpRequestKey]string{}
}

func (p *PendingAppServerRequests) NoteServerRequest(request ServerRequest) *UnsupportedAppServerRequest {
	if p == nil {
		return nil
	}
	p.ensure()
	switch request.Kind {
	case ServerRequestCommandExecutionApproval:
		id := request.ApprovalID
		if id == "" {
			id = request.ItemID
		}
		p.execApprovals[pendingApprovalKey{threadID: canonicalApprovalThreadID(request.ThreadID), itemID: id}] = request.ID
	case ServerRequestFileChangeApproval:
		p.fileChangeApprovals[pendingApprovalKey{threadID: canonicalApprovalThreadID(request.ThreadID), itemID: request.ItemID}] = request.ID
	case ServerRequestPermissionsApproval:
		p.permissionsApprovals[pendingApprovalKey{threadID: canonicalApprovalThreadID(request.ThreadID), itemID: request.ItemID}] = request.ID
	case ServerRequestUserInput:
		p.userInputs[request.TurnID] = append(p.userInputs[request.TurnID], pendingUserInputRequest{
			ItemID:    request.ItemID,
			RequestID: request.ID,
		})
	case ServerRequestMcpElicitation:
		key := mcpRequestKey{ServerName: request.ServerName, RequestID: request.ID}
		p.mcpRequests[key] = request.ID
	case ServerRequestDynamicToolCall:
		return &UnsupportedAppServerRequest{RequestID: request.ID, Message: "Dynamic tool calls are not available in TUI yet."}
	case ServerRequestAttestationGenerate:
		return &UnsupportedAppServerRequest{RequestID: request.ID, Message: "Attestation generation is not available in TUI."}
	case ServerRequestCurrentTimeRead:
		return &UnsupportedAppServerRequest{RequestID: request.ID, Message: "External current time is not available in TUI."}
	case ServerRequestApplyPatchApproval:
		return &UnsupportedAppServerRequest{RequestID: request.ID, Message: "Legacy patch approval requests are not available in TUI yet."}
	case ServerRequestExecCommandApproval:
		return &UnsupportedAppServerRequest{RequestID: request.ID, Message: "Legacy command approval requests are not available in TUI yet."}
	case ServerRequestChatGPTAuthTokensRefresh:
		return nil
	}
	return nil
}

func (p *PendingAppServerRequests) ContainsServerRequest(request ServerRequest) bool {
	if p == nil {
		return false
	}
	p.ensure()
	switch request.Kind {
	case ServerRequestCommandExecutionApproval:
		id := request.ApprovalID
		if id == "" {
			id = request.ItemID
		}
		return mapContainsValue(p.execApprovals, request.ID) && p.execApprovals[pendingApprovalKey{threadID: canonicalApprovalThreadID(request.ThreadID), itemID: id}] == request.ID
	case ServerRequestFileChangeApproval:
		return p.fileChangeApprovals[pendingApprovalKey{threadID: canonicalApprovalThreadID(request.ThreadID), itemID: request.ItemID}] == request.ID
	case ServerRequestPermissionsApproval:
		return p.permissionsApprovals[pendingApprovalKey{threadID: canonicalApprovalThreadID(request.ThreadID), itemID: request.ItemID}] == request.ID
	case ServerRequestUserInput:
		for _, queue := range p.userInputs {
			for _, pending := range queue {
				if pending.RequestID == request.ID {
					return true
				}
			}
		}
		return false
	case ServerRequestMcpElicitation:
		return mapContainsValueMCP(p.mcpRequests, request.ID)
	case ServerRequestDynamicToolCall,
		ServerRequestChatGPTAuthTokensRefresh,
		ServerRequestAttestationGenerate,
		ServerRequestCurrentTimeRead,
		ServerRequestApplyPatchApproval,
		ServerRequestExecCommandApproval:
		return true
	default:
		return false
	}
}

func (p *PendingAppServerRequests) ResolveNotification(requestID string) (ResolvedAppServerRequest, bool) {
	if p == nil {
		return ResolvedAppServerRequest{}, false
	}
	p.ensure()
	for key, pendingRequestID := range p.execApprovals {
		if pendingRequestID == requestID {
			delete(p.execApprovals, key)
			return ResolvedAppServerRequest{Kind: ResolvedAppServerRequestExecApproval, ID: key.itemID, ThreadID: key.threadID, RequestID: requestID}, true
		}
	}
	for key, pendingRequestID := range p.fileChangeApprovals {
		if pendingRequestID == requestID {
			delete(p.fileChangeApprovals, key)
			return ResolvedAppServerRequest{Kind: ResolvedAppServerRequestFileChangeApproval, ID: key.itemID, ThreadID: key.threadID, RequestID: requestID}, true
		}
	}
	for key, pendingRequestID := range p.permissionsApprovals {
		if pendingRequestID == requestID {
			delete(p.permissionsApprovals, key)
			return ResolvedAppServerRequest{Kind: ResolvedAppServerRequestPermissionsApproval, ID: key.itemID, ThreadID: key.threadID, RequestID: requestID}, true
		}
	}
	if pending, ok := p.removeUserInputRequest(requestID); ok {
		return ResolvedAppServerRequest{Kind: ResolvedAppServerRequestUserInput, CallID: pending.ItemID, RequestID: requestID}, true
	}
	for key, pendingRequestID := range p.mcpRequests {
		if pendingRequestID == requestID {
			delete(p.mcpRequests, key)
			return ResolvedAppServerRequest{Kind: ResolvedAppServerRequestMcpElicitation, ServerName: key.ServerName, RequestID: key.RequestID}, true
		}
	}
	return ResolvedAppServerRequest{}, false
}

func (p *PendingAppServerRequests) PopUserInputRequestForTurn(turnID string) (string, bool) {
	if p == nil {
		return "", false
	}
	p.ensure()
	queue := p.userInputs[turnID]
	if len(queue) == 0 {
		return "", false
	}
	pending := queue[0]
	queue = queue[1:]
	if len(queue) == 0 {
		delete(p.userInputs, turnID)
	} else {
		p.userInputs[turnID] = queue
	}
	return pending.RequestID, true
}

func (p *PendingAppServerRequests) PendingCount() int {
	if p == nil {
		return 0
	}
	p.ensure()
	count := len(p.execApprovals) + len(p.fileChangeApprovals) + len(p.permissionsApprovals) + len(p.mcpRequests)
	for _, queue := range p.userInputs {
		count += len(queue)
	}
	return count
}

func (p *PendingAppServerRequests) removeUserInputRequest(requestID string) (pendingUserInputRequest, bool) {
	for turnID, queue := range p.userInputs {
		for i, pending := range queue {
			if pending.RequestID == requestID {
				queue = append(queue[:i], queue[i+1:]...)
				if len(queue) == 0 {
					delete(p.userInputs, turnID)
				} else {
					p.userInputs[turnID] = queue
				}
				return pending, true
			}
		}
	}
	return pendingUserInputRequest{}, false
}

func (p *PendingAppServerRequests) ensure() {
	if p.execApprovals == nil {
		p.execApprovals = map[pendingApprovalKey]string{}
	}
	if p.fileChangeApprovals == nil {
		p.fileChangeApprovals = map[pendingApprovalKey]string{}
	}
	if p.permissionsApprovals == nil {
		p.permissionsApprovals = map[pendingApprovalKey]string{}
	}
	if p.userInputs == nil {
		p.userInputs = map[string][]pendingUserInputRequest{}
	}
	if p.mcpRequests == nil {
		p.mcpRequests = map[mcpRequestKey]string{}
	}
}

// canonicalApprovalThreadID normalizes parseable thread ids so equivalent
// UUID representations still match (Rust #39372).
func canonicalApprovalThreadID(threadID string) string {
	parsed, ok := ParseAppServerThreadID(threadID)
	if ok {
		return parsed
	}
	return strings.TrimSpace(threadID)
}

func mapContainsValue(values map[pendingApprovalKey]string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func mapContainsValueMCP(values map[mcpRequestKey]string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
