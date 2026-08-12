package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/sandbox"
)

type ServerRequestMethod string

const (
	ServerRequestCommandExecutionApproval ServerRequestMethod = "item/commandExecution/requestApproval"
	ServerRequestFileChangeApproval       ServerRequestMethod = "item/fileChange/requestApproval"
	ServerRequestToolUserInput            ServerRequestMethod = "item/tool/requestUserInput"
	ServerRequestMCPElicitation           ServerRequestMethod = "mcpServer/elicitation/request"
	ServerRequestPermissionsApproval      ServerRequestMethod = "item/permissions/requestApproval"
	ServerRequestDynamicToolCall          ServerRequestMethod = "item/tool/call"
	ServerRequestChatGPTAuthTokensRefresh ServerRequestMethod = "account/chatgptAuthTokens/refresh"
	ServerRequestAttestationGenerate      ServerRequestMethod = "attestation/generate"
	ServerRequestCurrentTimeRead          ServerRequestMethod = "currentTime/read"
	ServerRequestApplyPatchApproval       ServerRequestMethod = "applyPatchApproval"
	ServerRequestExecCommandApproval      ServerRequestMethod = "execCommandApproval"
)

type ServerRequest struct {
	JSONRPC string              `json:"jsonrpc,omitempty"`
	ID      RequestID           `json:"id"`
	Method  ServerRequestMethod `json:"method"`
	Params  any                 `json:"params,omitempty"`
}

func serverRequestThreadID(request *ServerRequest) string {
	if request == nil {
		return ""
	}
	switch params := request.Params.(type) {
	case *CommandExecutionRequestApprovalParams:
		if params != nil {
			return strings.TrimSpace(params.ThreadID)
		}
	case CommandExecutionRequestApprovalParams:
		return strings.TrimSpace(params.ThreadID)
	case *FileChangeRequestApprovalParams:
		if params != nil {
			return strings.TrimSpace(params.ThreadID)
		}
	case FileChangeRequestApprovalParams:
		return strings.TrimSpace(params.ThreadID)
	case *ToolRequestUserInputParams:
		if params != nil {
			return strings.TrimSpace(params.ThreadID)
		}
	case ToolRequestUserInputParams:
		return strings.TrimSpace(params.ThreadID)
	case *PermissionsRequestApprovalParams:
		if params != nil {
			return strings.TrimSpace(params.ThreadID)
		}
	case PermissionsRequestApprovalParams:
		return strings.TrimSpace(params.ThreadID)
	case *DynamicToolCallParams:
		if params != nil {
			return strings.TrimSpace(params.ThreadID)
		}
	case DynamicToolCallParams:
		return strings.TrimSpace(params.ThreadID)
	case *MCPElicitationRequestParams:
		if params != nil {
			return strings.TrimSpace(params.ThreadID)
		}
	case MCPElicitationRequestParams:
		return strings.TrimSpace(params.ThreadID)
	}
	return ""
}

type CommandExecutionRequestApprovalParams struct {
	ThreadID                        string                               `json:"threadId"`
	TurnID                          string                               `json:"turnId"`
	ItemID                          string                               `json:"itemId"`
	StartedAtMS                     uint64                               `json:"startedAtMs"`
	ApprovalID                      *string                              `json:"approvalId,omitempty"`
	EnvironmentID                   *string                              `json:"environmentId"`
	Reason                          *string                              `json:"reason,omitempty"`
	NetworkApprovalContext          any                                  `json:"networkApprovalContext,omitempty"`
	Command                         *string                              `json:"command,omitempty"`
	CWD                             *string                              `json:"cwd,omitempty"`
	CommandActions                  []map[string]any                     `json:"commandActions,omitempty"`
	ProposedExecPolicyAmendment     any                                  `json:"proposedExecpolicyAmendment,omitempty"`
	ProposedNetworkPolicyAmendments []map[string]any                     `json:"proposedNetworkPolicyAmendments,omitempty"`
	Action                          map[string]any                       `json:"action,omitempty"`
	SuggestedProfile                *string                              `json:"suggestedProfile,omitempty"`
	SandboxDenied                   bool                                 `json:"sandboxDenied,omitempty"`
	UserApprovalMessage             *string                              `json:"userApprovalMessage,omitempty"`
	AdditionalPermissions           *sandbox.AdditionalPermissionProfile `json:"-"`
}

func (p *CommandExecutionRequestApprovalParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID                        string           `json:"threadId"`
		TurnID                          string           `json:"turnId"`
		ItemID                          string           `json:"itemId"`
		StartedAtMS                     uint64           `json:"startedAtMs"`
		ApprovalID                      *string          `json:"approvalId,omitempty"`
		EnvironmentID                   *string          `json:"environmentId"`
		Reason                          *string          `json:"reason,omitempty"`
		NetworkApprovalContext          any              `json:"networkApprovalContext,omitempty"`
		Command                         *string          `json:"command,omitempty"`
		CWD                             *string          `json:"cwd,omitempty"`
		CommandActions                  []map[string]any `json:"commandActions,omitempty"`
		ProposedExecPolicyAmendment     any              `json:"proposedExecpolicyAmendment,omitempty"`
		ProposedNetworkPolicyAmendments []map[string]any `json:"proposedNetworkPolicyAmendments,omitempty"`
	}{
		ThreadID:                        p.ThreadID,
		TurnID:                          p.TurnID,
		ItemID:                          p.ItemID,
		StartedAtMS:                     p.StartedAtMS,
		ApprovalID:                      p.ApprovalID,
		EnvironmentID:                   p.EnvironmentID,
		Reason:                          p.Reason,
		NetworkApprovalContext:          p.NetworkApprovalContext,
		Command:                         p.Command,
		CWD:                             p.CWD,
		CommandActions:                  append([]map[string]any(nil), p.CommandActions...),
		ProposedExecPolicyAmendment:     p.ProposedExecPolicyAmendment,
		ProposedNetworkPolicyAmendments: append([]map[string]any(nil), p.ProposedNetworkPolicyAmendments...),
	})
}

type FileChangeRequestApprovalParams struct {
	ThreadID    string  `json:"threadId"`
	TurnID      string  `json:"turnId"`
	ItemID      string  `json:"itemId"`
	StartedAtMS uint64  `json:"startedAtMs"`
	Reason      *string `json:"reason,omitempty"`
	GrantRoot   *string `json:"grantRoot,omitempty"`
}

func (p *FileChangeRequestApprovalParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID    string  `json:"threadId"`
		TurnID      string  `json:"turnId"`
		ItemID      string  `json:"itemId"`
		StartedAtMS uint64  `json:"startedAtMs"`
		Reason      *string `json:"reason,omitempty"`
		GrantRoot   *string `json:"grantRoot,omitempty"`
	}{
		ThreadID:    p.ThreadID,
		TurnID:      p.TurnID,
		ItemID:      p.ItemID,
		StartedAtMS: p.StartedAtMS,
		Reason:      p.Reason,
		GrantRoot:   p.GrantRoot,
	})
}

type ToolRequestUserInputParams struct {
	ThreadID         string                         `json:"threadId"`
	TurnID           string                         `json:"turnId"`
	ItemID           string                         `json:"itemId"`
	Questions        []ToolRequestUserInputQuestion `json:"questions"`
	AutoResolutionMS *uint64                        `json:"autoResolutionMs"`
	Question         ToolRequestUserInputQuestion   `json:"question,omitempty"`
	Timeout          *ToolRequestUserInputTimeoutMS `json:"timeout,omitempty"`
}

type ToolRequestUserInputQuestion struct {
	ID       string                       `json:"id"`
	Header   string                       `json:"header"`
	Question string                       `json:"question"`
	IsOther  bool                         `json:"isOther,omitempty"`
	IsSecret bool                         `json:"isSecret,omitempty"`
	Options  []ToolRequestUserInputOption `json:"options"`
	Prompt   string                       `json:"prompt,omitempty"`
}

func (p *ToolRequestUserInputParams) MarshalJSON() ([]byte, error) {
	questions := append([]ToolRequestUserInputQuestion(nil), p.Questions...)
	if len(questions) == 0 && (p.Question.ID != "" || p.Question.Question != "" || p.Question.Prompt != "") {
		questions = []ToolRequestUserInputQuestion{p.Question}
	}
	if questions == nil {
		questions = []ToolRequestUserInputQuestion{}
	}
	autoResolution := p.AutoResolutionMS
	if autoResolution == nil && p.Timeout != nil {
		value := uint64(*p.Timeout)
		autoResolution = &value
	}
	return json.Marshal(struct {
		ThreadID         string                         `json:"threadId"`
		TurnID           string                         `json:"turnId"`
		ItemID           string                         `json:"itemId"`
		Questions        []ToolRequestUserInputQuestion `json:"questions"`
		AutoResolutionMS *uint64                        `json:"autoResolutionMs"`
	}{
		ThreadID:         p.ThreadID,
		TurnID:           p.TurnID,
		ItemID:           p.ItemID,
		Questions:        questions,
		AutoResolutionMS: autoResolution,
	})
}

func (q *ToolRequestUserInputQuestion) MarshalJSON() ([]byte, error) {
	options := append([]ToolRequestUserInputOption(nil), q.Options...)
	var optionsPtr []ToolRequestUserInputOption
	if options != nil {
		optionsPtr = options
	}
	question := q.Question
	if question == "" {
		question = q.Prompt
	}
	header := q.Header
	return json.Marshal(struct {
		ID       string                       `json:"id"`
		Header   string                       `json:"header"`
		Question string                       `json:"question"`
		IsOther  bool                         `json:"isOther"`
		IsSecret bool                         `json:"isSecret"`
		Options  []ToolRequestUserInputOption `json:"options"`
	}{
		ID:       q.ID,
		Header:   header,
		Question: question,
		IsOther:  q.IsOther,
		IsSecret: q.IsSecret,
		Options:  optionsPtr,
	})
}

type ToolRequestUserInputOption struct {
	ID          string `json:"id,omitempty"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (o ToolRequestUserInputOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	}{
		Label:       o.Label,
		Description: o.Description,
	})
}

type ToolRequestUserInputTimeoutMS uint64

type PermissionsRequestApprovalParams struct {
	ThreadID      string         `json:"threadId"`
	TurnID        string         `json:"turnId"`
	ItemID        string         `json:"itemId"`
	EnvironmentID *string        `json:"environmentId"`
	StartedAtMS   uint64         `json:"startedAtMs"`
	CWD           string         `json:"cwd"`
	Reason        *string        `json:"reason"`
	Permissions   map[string]any `json:"permissions"`
}

type DynamicToolCallParams struct {
	ThreadID  string  `json:"threadId"`
	TurnID    string  `json:"turnId"`
	CallID    string  `json:"callId"`
	Namespace *string `json:"namespace"`
	Tool      string  `json:"tool"`
	Arguments any     `json:"arguments"`
	ToolName  string  `json:"toolName,omitempty"`
	Input     any     `json:"input,omitempty"`
}

func (p *DynamicToolCallParams) MarshalJSON() ([]byte, error) {
	tool := p.Tool
	if tool == "" {
		tool = p.ToolName
	}
	arguments := p.Arguments
	if arguments == nil {
		arguments = p.Input
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return json.Marshal(struct {
		ThreadID  string  `json:"threadId"`
		TurnID    string  `json:"turnId"`
		CallID    string  `json:"callId"`
		Namespace *string `json:"namespace"`
		Tool      string  `json:"tool"`
		Arguments any     `json:"arguments"`
	}{
		ThreadID:  p.ThreadID,
		TurnID:    p.TurnID,
		CallID:    p.CallID,
		Namespace: p.Namespace,
		Tool:      tool,
		Arguments: arguments,
	})
}

type MCPElicitationRequestParams struct {
	ThreadID        string  `json:"threadId"`
	TurnID          *string `json:"turnId"`
	ServerName      string  `json:"serverName"`
	Mode            string  `json:"mode"`
	Meta            any     `json:"_meta"`
	Message         string  `json:"message"`
	RequestedSchema any     `json:"requestedSchema,omitempty"`
	URL             string  `json:"url,omitempty"`
	ElicitationID   string  `json:"elicitationId,omitempty"`
	Server          string  `json:"server,omitempty"`
	Schema          any     `json:"schema,omitempty"`
}

func (p *MCPElicitationRequestParams) MarshalJSON() ([]byte, error) {
	serverName := p.ServerName
	if serverName == "" {
		serverName = p.Server
	}
	mode := p.Mode
	if mode == "" {
		mode = "form"
	}
	requestedSchema := p.RequestedSchema
	if requestedSchema == nil {
		requestedSchema = p.Schema
	}
	if requestedSchema == nil && mode != "url" {
		requestedSchema = map[string]any{}
	}
	return json.Marshal(struct {
		ThreadID        string  `json:"threadId"`
		TurnID          *string `json:"turnId"`
		ServerName      string  `json:"serverName"`
		Mode            string  `json:"mode"`
		Meta            any     `json:"_meta"`
		Message         string  `json:"message"`
		RequestedSchema any     `json:"requestedSchema,omitempty"`
		URL             string  `json:"url,omitempty"`
		ElicitationID   string  `json:"elicitationId,omitempty"`
	}{
		ThreadID:        p.ThreadID,
		TurnID:          p.TurnID,
		ServerName:      serverName,
		Mode:            mode,
		Meta:            p.Meta,
		Message:         p.Message,
		RequestedSchema: requestedSchema,
		URL:             p.URL,
		ElicitationID:   p.ElicitationID,
	})
}

type AttestationGenerateParams struct{}

type AttestationGenerateResponse struct {
	Token string `json:"token"`
}

type CurrentTimeReadParams struct {
	ThreadID string `json:"threadId"`
}

type CurrentTimeReadResponse struct {
	CurrentTimeAt int64 `json:"currentTimeAt"`
}

type ChatGPTAuthTokensRefreshResponse struct {
	AccessToken      string  `json:"accessToken"`
	ChatGPTAccountID string  `json:"chatgptAccountId"`
	ChatGPTPlanType  *string `json:"chatgptPlanType"`
}

type ApplyPatchApprovalParams struct {
	ConversationID string         `json:"conversationId"`
	CallID         string         `json:"callId"`
	FileChanges    map[string]any `json:"fileChanges"`
	Reason         *string        `json:"reason"`
	GrantRoot      *string        `json:"grantRoot"`
}

type ApplyPatchApprovalResponse struct {
	Decision any `json:"decision"`
}

func (p *ApplyPatchApprovalParams) MarshalJSON() ([]byte, error) {
	fileChanges := cloneAnyMapAppserver(p.FileChanges)
	if fileChanges == nil {
		fileChanges = map[string]any{}
	}
	return json.Marshal(struct {
		ConversationID string         `json:"conversationId"`
		CallID         string         `json:"callId"`
		FileChanges    map[string]any `json:"fileChanges"`
		Reason         *string        `json:"reason"`
		GrantRoot      *string        `json:"grantRoot"`
	}{
		ConversationID: p.ConversationID,
		CallID:         p.CallID,
		FileChanges:    fileChanges,
		Reason:         p.Reason,
		GrantRoot:      p.GrantRoot,
	})
}

type ReviewDecision string

const (
	ReviewDecisionApproved           ReviewDecision = "approved"
	ReviewDecisionApprovedForSession ReviewDecision = "approved_for_session"
	// Rust 67afc79674: MCP tool approval that persists as a policy amendment
	// across sessions. Non-MCP approval paths reject this decision.
	ReviewDecisionApprovedMcpPolicyAmendment ReviewDecision = "approved_mcp_policy_amendment"
	ReviewDecisionDenied                     ReviewDecision = "denied"
	ReviewDecisionTimedOut                   ReviewDecision = "timed_out"
	ReviewDecisionAbort                      ReviewDecision = "abort"
)

type ExecCommandApprovalParams struct {
	ConversationID string   `json:"conversationId"`
	CallID         string   `json:"callId"`
	ApprovalID     *string  `json:"approvalId"`
	Command        []string `json:"command"`
	CWD            string   `json:"cwd"`
	ParsedCmd      []any    `json:"parsedCmd"`
	Reason         *string  `json:"reason"`
}

type ExecCommandApprovalResponse struct {
	Decision any `json:"decision"`
}

type CommandExecutionRequestApprovalResponse struct {
	Decision any `json:"decision"`
}

type CommandExecutionApprovalDecision string

const (
	CommandExecutionApprovalAccept                        CommandExecutionApprovalDecision = "accept"
	CommandExecutionApprovalAcceptForSession              CommandExecutionApprovalDecision = "acceptForSession"
	CommandExecutionApprovalAcceptWithExecpolicyAmendment CommandExecutionApprovalDecision = "acceptWithExecpolicyAmendment"
	CommandExecutionApprovalApplyNetworkPolicyAmendment   CommandExecutionApprovalDecision = "applyNetworkPolicyAmendment"
	CommandExecutionApprovalDecline                       CommandExecutionApprovalDecision = "decline"
	CommandExecutionApprovalCancel                        CommandExecutionApprovalDecision = "cancel"
)

type FileChangeRequestApprovalResponse struct {
	Decision any `json:"decision"`
}

type FileChangeApprovalDecision string

const (
	FileChangeApprovalAccept           FileChangeApprovalDecision = "accept"
	FileChangeApprovalAcceptForSession FileChangeApprovalDecision = "acceptForSession"
	FileChangeApprovalDecline          FileChangeApprovalDecision = "decline"
	FileChangeApprovalCancel           FileChangeApprovalDecision = "cancel"
)

type MCPElicitationAction string

const (
	MCPElicitationActionAccept  MCPElicitationAction = "accept"
	MCPElicitationActionDecline MCPElicitationAction = "decline"
	MCPElicitationActionCancel  MCPElicitationAction = "cancel"
)

type MCPElicitationRequestResponse struct {
	Action  MCPElicitationAction `json:"action"`
	Content any                  `json:"content"`
	Meta    any                  `json:"_meta"`
}

func (p *ExecCommandApprovalParams) MarshalJSON() ([]byte, error) {
	command := append([]string(nil), p.Command...)
	if command == nil {
		command = []string{}
	}
	parsedCmd := append([]any(nil), p.ParsedCmd...)
	if parsedCmd == nil {
		parsedCmd = []any{}
	}
	return json.Marshal(struct {
		ConversationID string   `json:"conversationId"`
		CallID         string   `json:"callId"`
		ApprovalID     *string  `json:"approvalId"`
		Command        []string `json:"command"`
		CWD            string   `json:"cwd"`
		ParsedCmd      []any    `json:"parsedCmd"`
		Reason         *string  `json:"reason"`
	}{
		ConversationID: p.ConversationID,
		CallID:         p.CallID,
		ApprovalID:     p.ApprovalID,
		Command:        command,
		CWD:            p.CWD,
		ParsedCmd:      parsedCmd,
		Reason:         p.Reason,
	})
}

type ToolRequestUserInputResponse struct {
	Answers map[string]ToolRequestUserInputAnswer `json:"answers"`
}

func (r *ToolRequestUserInputResponse) MarshalJSON() ([]byte, error) {
	answers := map[string]ToolRequestUserInputAnswer{}
	for key, value := range r.Answers {
		answers[key] = value
	}
	return json.Marshal(struct {
		Answers map[string]ToolRequestUserInputAnswer `json:"answers"`
	}{Answers: answers})
}

type ToolRequestUserInputAnswer struct {
	Answers []string `json:"answers"`
}

func (a *ToolRequestUserInputAnswer) MarshalJSON() ([]byte, error) {
	answers := append([]string(nil), a.Answers...)
	if answers == nil {
		answers = []string{}
	}
	return json.Marshal(struct {
		Answers []string `json:"answers"`
	}{
		Answers: answers,
	})
}

type DynamicToolCallResponse struct {
	Success      bool                           `json:"success"`
	ContentItems []DynamicToolCallOutputContent `json:"contentItems"`
}

func (r *DynamicToolCallResponse) MarshalJSON() ([]byte, error) {
	contentItems := append([]DynamicToolCallOutputContent(nil), r.ContentItems...)
	if contentItems == nil {
		contentItems = []DynamicToolCallOutputContent{}
	}
	return json.Marshal(struct {
		Success      bool                           `json:"success"`
		ContentItems []DynamicToolCallOutputContent `json:"contentItems"`
	}{
		Success:      r.Success,
		ContentItems: contentItems,
	})
}

type DynamicToolCallOutputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
	AudioURL string `json:"audioUrl,omitempty"`
}

func (c *DynamicToolCallOutputContent) MarshalJSON() ([]byte, error) {
	contentType := c.Type
	if contentType == "" {
		if c.ImageURL != "" {
			contentType = "inputImage"
		} else {
			contentType = "inputText"
		}
	}
	if contentType == "inputImage" {
		return json.Marshal(struct {
			Type     string `json:"type"`
			ImageURL string `json:"imageUrl"`
		}{
			Type:     contentType,
			ImageURL: c.ImageURL,
		})
	}
	if contentType == "inputAudio" {
		return json.Marshal(struct {
			Type     string `json:"type"`
			AudioURL string `json:"audioUrl"`
		}{Type: contentType, AudioURL: c.AudioURL})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{
		Type: contentType,
		Text: c.Text,
	})
}

type PermissionsRequestApprovalResponse struct {
	Permissions      *GrantedPermissionProfile `json:"permissions"`
	Scope            PermissionGrantScope      `json:"scope"`
	StrictAutoReview *bool                     `json:"strictAutoReview,omitempty"`
}

func (r *PermissionsRequestApprovalResponse) MarshalJSON() ([]byte, error) {
	permissions := r.Permissions
	if permissions == nil {
		permissions = &GrantedPermissionProfile{}
	}
	return json.Marshal(struct {
		Permissions      *GrantedPermissionProfile `json:"permissions"`
		Scope            PermissionGrantScope      `json:"scope"`
		StrictAutoReview *bool                     `json:"strictAutoReview,omitempty"`
	}{
		Permissions:      permissions,
		Scope:            r.Scope,
		StrictAutoReview: r.StrictAutoReview,
	})
}

type PermissionGrantScope string

const (
	PermissionGrantScopeTurn    PermissionGrantScope = "turn"
	PermissionGrantScopeSession PermissionGrantScope = "session"
)

type GrantedPermissionProfile struct {
	Network    *AdditionalNetworkPermissions    `json:"network,omitempty"`
	FileSystem *AdditionalFileSystemPermissions `json:"fileSystem,omitempty"`
}

func (p *GrantedPermissionProfile) MarshalJSON() ([]byte, error) {
	var network *AdditionalNetworkPermissions
	var fileSystem *AdditionalFileSystemPermissions
	if p != nil {
		network = p.Network
		fileSystem = p.FileSystem
	}
	return json.Marshal(struct {
		Network    *AdditionalNetworkPermissions    `json:"network"`
		FileSystem *AdditionalFileSystemPermissions `json:"fileSystem"`
	}{
		Network:    network,
		FileSystem: fileSystem,
	})
}

type AdditionalNetworkPermissions struct {
	Enabled *bool `json:"enabled"`
}

type AdditionalFileSystemPermissions struct {
	Read             []string `json:"read"`
	Write            []string `json:"write"`
	GlobScanMaxDepth *uint32  `json:"globScanMaxDepth,omitempty"`
	Entries          []any    `json:"entries,omitempty"`
}

func (p *AdditionalFileSystemPermissions) MarshalJSON() ([]byte, error) {
	read := []string{}
	write := []string{}
	var globScanMaxDepth *uint32
	var entries []any
	if p != nil {
		read = append(read, p.Read...)
		write = append(write, p.Write...)
		globScanMaxDepth = p.GlobScanMaxDepth
		entries = append([]any(nil), p.Entries...)
	}
	return json.Marshal(struct {
		Read             []string `json:"read"`
		Write            []string `json:"write"`
		GlobScanMaxDepth *uint32  `json:"globScanMaxDepth,omitempty"`
		Entries          []any    `json:"entries,omitempty"`
	}{
		Read:             read,
		Write:            write,
		GlobScanMaxDepth: globScanMaxDepth,
		Entries:          entries,
	})
}
