package mcp

type McpServerStartupState = MCPServerStartupState
type McpAuthStatus = MCPAuthStatus
type McpServerStatusDetail = MCPServerStatusDetail
type ListMcpServerStatusParams = MCPListServerStatusParams
type ListMcpServerStatusResponse = MCPListServerStatusResponse
type McpServerStatus = MCPServerStatus
type McpServerOauthLoginParams = MCPServerOauthLoginParams
type McpServerOauthLoginResponse = MCPServerOauthLoginResponse
type McpServerOauthCancelParams = MCPServerOauthCancelParams
type McpServerOauthCancelResponse = MCPServerOauthCancelResponse
type McpServerRefreshResponse = MCPServerRefreshResponse
type McpResourceReadParams = MCPResourceReadParams
type McpResourceReadResponse = MCPResourceReadResponse
type McpServerToolCallParams = MCPToolCallParams
type McpServerToolCallResponse = MCPToolCallResponse
type McpServerInfo = MCPServerInfo
type Resource = MCPResource
type ResourceContent = MCPResourceContent
type ResourceTemplate = MCPResourceTemplate

type McpToolCallProgressNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Message  string `json:"message"`
}
