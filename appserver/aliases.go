package appserver

import (
	"encoding/json"

	"codex_go/filesearch"
	mcptypes "codex_go/mcp"
)

type ClientRequest = Request
type ServerNotification = Notification

type ClientNotification struct {
	Method string `json:"method"`
}

const ClientNotificationInitialized = "initialized"

func (n *ClientNotification) MarshalJSON() ([]byte, error) {
	method := n.Method
	if method == "" {
		method = ClientNotificationInitialized
	}
	return json.Marshal(struct {
		Method string `json:"method"`
	}{Method: method})
}

type FsReadFileParams = ReadFileParams
type FsReadFileResponse = ReadFileResponse
type FsWriteFileParams = WriteFileParams
type FsWriteFileResponse = WriteFileResponse
type FsCreateDirectoryParams = CreateDirectoryParams
type FsCreateDirectoryResponse = CreateDirectoryResponse
type FsGetMetadataParams = GetMetadataParams
type FsGetMetadataResponse = GetMetadataResponse
type FsReadDirectoryParams = ReadDirectoryParams
type FsReadDirectoryEntry = ReadDirectoryEntry
type FsReadDirectoryResponse = ReadDirectoryResponse
type FsRemoveParams = RemoveParams
type FsRemoveResponse = RemoveResponse
type FsCopyParams = CopyParams
type FsCopyResponse = CopyResponse
type FsWatchParams = WatchParams
type FsWatchResponse = WatchResponse
type FsUnwatchParams = UnwatchParams
type FsUnwatchResponse = UnwatchResponse
type FsChangedNotification = ChangedNotification

type HooksListParams = HookListParams
type HooksListResponse = HookListResponse

type CommandExecTerminalSize = TerminalSize
type ProcessTerminalSize = TerminalSize
type CommandExecOutputStream = OutputStream
type ProcessOutputStream = OutputStream

type ThreadArchivedNotification = ThreadIDNotification
type ThreadDeletedNotification = ThreadIDNotification
type ThreadUnarchivedNotification = ThreadIDNotification
type ThreadClosedNotification = ThreadIDNotification
type ThreadGoalUpdatedNotification = GoalUpdatedNotification
type ThreadGoalClearedNotification = GoalClearedNotification
type ThreadSettingsUpdatedNotification = SettingsUpdatedNotification
type ThreadRealtimeSdpNotification = ThreadRealtimeSDPNotification

type McpServerStartupFailureReason = MCPServerStartupFailureReason
type McpServerStatusUpdatedNotification = MCPServerStatusUpdatedNotification
type McpToolCallProgressNotification = MCPToolCallProgressNotification
type McpServerOauthLoginCompletedNotification = MCPServerOauthLoginCompletedNotification
type McpServerElicitationAction = MCPElicitationAction
type McpServerElicitationRequestParams = MCPElicitationRequestParams
type McpServerElicitationRequestResponse = MCPElicitationRequestResponse
type DynamicToolCallOutputContentItem = DynamicToolCallOutputContent

type GetAuthStatusParams = AuthStatusParams
type GetAuthStatusResponse = AuthStatusResponse
type GetConversationSummaryParams = ConversationSummaryParams
type GetConversationSummaryResponse = ConversationSummaryResponse
type FuzzyFileSearchMatchType = filesearch.MatchType
type FuzzyFileSearchResult = filesearch.FileMatch
type Tool = mcptypes.MCPToolInfo
