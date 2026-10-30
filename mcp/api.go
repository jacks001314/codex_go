package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	managedconfig "codex_go/config"
	"codex_go/sandbox"
)

var ErrInvalidMCPRequest = errors.New("invalid mcp request")

const mcpToolThreadIDMetaKey = "threadId"

type invalidMCPRequestError struct {
	message string
}

func (e *invalidMCPRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *invalidMCPRequestError) Unwrap() error {
	return ErrInvalidMCPRequest
}

func invalidMCPRequest(message string) error {
	return &invalidMCPRequestError{message: message}
}

type MCPServerStartupState string

const (
	MCPServerStarting  MCPServerStartupState = "starting"
	MCPServerReady     MCPServerStartupState = "ready"
	MCPServerFailed    MCPServerStartupState = "failed"
	MCPServerCancelled MCPServerStartupState = "cancelled"
	MCPServerStopped   MCPServerStartupState = "stopped"
)

// MCPStartupObserver reports one server's startup transition. failureReason
// mirrors Rust's McpServerStartupFailureReason (for example
// "reauthenticationRequired") and is nil unless the server failed.
type MCPStartupObserver func(name string, status MCPServerStartupState, failureReason *string, err error)

type MCPAuthStatus string

const (
	MCPAuthUnknown     MCPAuthStatus = "unknown"
	MCPAuthUnsupported MCPAuthStatus = "unsupported"
	MCPAuthNotLoggedIn MCPAuthStatus = "notLoggedIn"
	MCPAuthBearerToken MCPAuthStatus = "bearerToken"
	MCPAuthOAuth       MCPAuthStatus = "oAuth"
)

// MCPConnectionStatus is the thread-runtime connection state of an MCP server
// (Rust #40068). It is reported on thread-scoped mcpServerStatus/list requests
// and is null when unavailable or the active configuration changed.
type MCPConnectionStatus string

const (
	MCPConnectionNotStarted             MCPConnectionStatus = "notStarted"
	MCPConnectionStarting               MCPConnectionStatus = "starting"
	MCPConnectionConnected              MCPConnectionStatus = "connected"
	MCPConnectionAuthenticationRequired MCPConnectionStatus = "authenticationRequired"
	MCPConnectionFailed                 MCPConnectionStatus = "failed"
	MCPConnectionCancelled              MCPConnectionStatus = "cancelled"
	MCPConnectionDisabled               MCPConnectionStatus = "disabled"
)

type MCPServerStatusDetailMode string

const (
	MCPServerStatusDetailFull             MCPServerStatusDetailMode = "full"
	MCPServerStatusDetailToolsAndAuthOnly MCPServerStatusDetailMode = "toolsAndAuthOnly"
)

type MCPServerStatusDetail struct {
	Mode               MCPServerStatusDetailMode `json:"-"`
	IncludeTools       bool                      `json:"includeTools,omitempty"`
	legacyIncludeTools *bool
}

func (d *MCPServerStatusDetail) UnmarshalJSON(data []byte) error {
	var mode MCPServerStatusDetailMode
	if err := json.Unmarshal(data, &mode); err == nil {
		switch mode {
		case MCPServerStatusDetailFull, MCPServerStatusDetailToolsAndAuthOnly:
			d.Mode = mode
			d.IncludeTools = true
			d.legacyIncludeTools = nil
			return nil
		}
		return fmt.Errorf("invalid MCP server status detail %q", mode)
	}
	var legacy struct {
		IncludeTools bool `json:"includeTools"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	d.Mode = ""
	d.IncludeTools = legacy.IncludeTools
	value := legacy.IncludeTools
	d.legacyIncludeTools = &value
	return nil
}

func (d *MCPServerStatusDetail) MarshalJSON() ([]byte, error) {
	mode := d.Mode
	if mode == "" {
		if d.IncludeTools {
			mode = MCPServerStatusDetailFull
		} else {
			mode = MCPServerStatusDetailToolsAndAuthOnly
		}
	}
	return json.Marshal(mode)
}

func (d *MCPServerStatusDetail) includesTools() bool {
	if d == nil {
		return true
	}
	if d.Mode == MCPServerStatusDetailFull || d.Mode == MCPServerStatusDetailToolsAndAuthOnly {
		return true
	}
	if d.legacyIncludeTools != nil {
		return *d.legacyIncludeTools
	}
	if !d.IncludeTools {
		return true
	}
	return d.IncludeTools
}

func (d *MCPServerStatusDetail) includesInventory() bool {
	if d == nil {
		return true
	}
	if d.Mode == MCPServerStatusDetailToolsAndAuthOnly {
		return false
	}
	if d.Mode == MCPServerStatusDetailFull {
		return true
	}
	if d.legacyIncludeTools != nil {
		return *d.legacyIncludeTools
	}
	return d.IncludeTools
}

type MCPListServerStatusParams struct {
	Cursor   *string                `json:"cursor,omitempty"`
	Limit    *uint32                `json:"limit,omitempty"`
	Detail   *MCPServerStatusDetail `json:"detail,omitempty"`
	ThreadID *string                `json:"threadId,omitempty"`
	// ServerName limits discovery to one server; combined with ThreadID the
	// thread's existing MCP connection is reused (Rust #48783).
	ServerName *string `json:"serverName,omitempty"`

	// Turn-scoped catalog capture can stop waiting for optional startup after a
	// shared grace period. Management RPCs leave NonBlockingOptional false.
	RequiredServers      []string      `json:"-"`
	RequiredPlugins      []string      `json:"-"`
	NonBlockingOptional  bool          `json:"-"`
	OptionalStartupGrace time.Duration `json:"-"`
}

type MCPServerInfo struct {
	Name        string   `json:"name"`
	Title       *string  `json:"title"`
	Version     string   `json:"version"`
	Description *string  `json:"description"`
	Icons       []any    `json:"icons"`
	WebsiteURL  *string  `json:"websiteUrl"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
}

func (i *MCPServerInfo) MarshalJSON() ([]byte, error) {
	if i == nil {
		return []byte("null"), nil
	}
	var icons []any
	if i.Icons != nil {
		icons = append([]any(nil), i.Icons...)
	}
	return json.Marshal(struct {
		Name        string  `json:"name"`
		Title       *string `json:"title"`
		Version     string  `json:"version"`
		Description *string `json:"description"`
		Icons       []any   `json:"icons"`
		WebsiteURL  *string `json:"websiteUrl"`
	}{
		Name:        i.Name,
		Title:       cloneStringPtr(i.Title),
		Version:     i.Version,
		Description: cloneStringPtr(i.Description),
		Icons:       icons,
		WebsiteURL:  cloneStringPtr(i.WebsiteURL),
	})
}

type MCPToolInfo struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema any            `json:"outputSchema,omitempty"`
	Annotations  any            `json:"annotations,omitempty"`
	Icons        []any          `json:"icons,omitempty"`
	Meta         any            `json:"_meta,omitempty"`
}

func (t MCPToolInfo) MarshalJSON() ([]byte, error) {
	inputSchema := cloneAnyMap(t.InputSchema)
	if inputSchema == nil {
		inputSchema = map[string]any{}
	}
	return json.Marshal(struct {
		Name         string         `json:"name"`
		Title        string         `json:"title,omitempty"`
		Description  string         `json:"description,omitempty"`
		InputSchema  map[string]any `json:"inputSchema"`
		OutputSchema any            `json:"outputSchema,omitempty"`
		Annotations  any            `json:"annotations,omitempty"`
		Icons        []any          `json:"icons,omitempty"`
		Meta         any            `json:"_meta,omitempty"`
	}{
		Name:         t.Name,
		Title:        t.Title,
		Description:  t.Description,
		InputSchema:  inputSchema,
		OutputSchema: cloneJSONValue(t.OutputSchema),
		Annotations:  cloneJSONValue(t.Annotations),
		Icons:        append([]any(nil), t.Icons...),
		Meta:         cloneJSONValue(t.Meta),
	})
}

type MCPServerStatus struct {
	Name string `json:"name,omitempty"`
	// source records the catalog registration source used to decide whether a
	// plugin-contributed server is a selected-plugin server. It is intentionally
	// internal: the wire status uses PluginID for plugin attribution instead.
	source CatalogSource
	// hostOwnedApps marks the host-owned codex_apps registration (Rust #51421);
	// internal-only.
	hostOwnedApps bool
	// catalogSource records how this status's tool catalog was obtained for the
	// raw-catalog size telemetry (Rust's `catalog_source`, #51215):
	// BindingCatalogSourceLive when this read queried the server, and
	// BindingCatalogSourceCached when an already-materialized catalog was
	// reused. Empty means this call did not materialize the catalog.
	// Internal-only: the wire status never carries it.
	catalogSource string
	// RuntimeStatus reports the current thread-runtime connection state; nil
	// when unavailable or the active configuration changed (Rust #40068).
	RuntimeStatus *MCPConnectionStatus `json:"runtimeStatus,omitempty"`
	// PluginID reports the owning plugin for plugin-contributed servers and is
	// nil for servers from other sources (Rust 78d3665d15).
	PluginID *string `json:"pluginId,omitempty"`
	// pluginHostRoot is the installed root of the plugin registration that won
	// this server name (Rust #58378 AgentMcpMetadataRecipient matching).
	// Internal-only: the wire status does not expose it.
	pluginHostRoot string
	ServerInfo     *MCPServerInfo `json:"serverInfo,omitempty"`
	// ServerCapabilities carries the initialized server's advertised
	// capabilities object, or null when unavailable (Rust #44826).
	ServerCapabilities json.RawMessage `json:"serverCapabilities,omitempty"`
	// ToolListMeta carries the opaque `_meta` of the first page of the tool
	// catalog this status was built from, or null when unavailable
	// (Rust #54686).
	ToolListMeta      json.RawMessage       `json:"toolListMeta,omitempty"`
	Tools             []MCPToolInfo         `json:"tools,omitempty"`
	ToolsError        *string               `json:"toolsError,omitempty"`
	Resources         []MCPResource         `json:"resources,omitempty"`
	ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates,omitempty"`
	AuthStatus        MCPAuthStatus         `json:"authStatus,omitempty"`
	Server            MCPServerInfo         `json:"server"`
	State             MCPServerStartupState `json:"state"`
	Error             *string               `json:"error,omitempty"`
	FailureReason     *string               `json:"failureReason,omitempty"`
}

func (s *MCPServerStatus) MarshalJSON() ([]byte, error) {
	name := s.Name
	if name == "" {
		name = s.Server.Name
	}
	serverInfo := s.ServerInfo
	if serverInfo == nil && (s.Server.Name != "" || s.Server.Version != "" || s.Server.Command != "") {
		info := s.Server
		serverInfo = &info
	}
	authStatus := s.AuthStatus
	if authStatus == "" {
		authStatus = MCPAuthUnsupported
	}
	state := s.State
	if state == "" {
		state = MCPServerReady
	}
	return json.Marshal(struct {
		Name               string                            `json:"name"`
		RuntimeStatus      *MCPConnectionStatus              `json:"runtimeStatus"`
		PluginID           *string                           `json:"pluginId"`
		ServerInfo         *MCPServerInfo                    `json:"serverInfo"`
		ServerCapabilities json.RawMessage                   `json:"serverCapabilities"`
		ToolListMeta       json.RawMessage                   `json:"toolListMeta"`
		Tools              map[string]MCPToolInfo            `json:"tools"`
		ToolsError         *string                           `json:"toolsError"`
		Resources          []mcpServerStatusResource         `json:"resources"`
		ResourceTemplates  []mcpServerStatusResourceTemplate `json:"resourceTemplates"`
		AuthStatus         MCPAuthStatus                     `json:"authStatus"`
		State              MCPServerStartupState             `json:"state"`
		Error              *string                           `json:"error,omitempty"`
		FailureReason      *string                           `json:"failureReason,omitempty"`
	}{
		Name:               name,
		RuntimeStatus:      cloneMCPConnectionStatus(s.RuntimeStatus),
		PluginID:           cloneStringPtr(s.PluginID),
		ServerInfo:         serverInfo,
		ServerCapabilities: cloneMCPRawMessage(s.ServerCapabilities),
		ToolListMeta:       cloneMCPRawMessage(s.ToolListMeta),
		Tools:              toolMapFromList(s.Tools),
		ToolsError:         cloneStringPtr(s.ToolsError),
		Resources:          mcpServerStatusResources(s.Resources),
		ResourceTemplates:  mcpServerStatusResourceTemplates(s.ResourceTemplates),
		AuthStatus:         authStatus,
		State:              state,
		Error:              cloneStringPtr(s.Error),
		FailureReason:      cloneStringPtr(s.FailureReason),
	})
}

type MCPListServerStatusResponse struct {
	Data       []MCPServerStatus `json:"data"`
	NextCursor *string           `json:"nextCursor"`
	Servers    []MCPServerStatus `json:"servers,omitempty"`
}

func (r *MCPListServerStatusResponse) MarshalJSON() ([]byte, error) {
	data := append([]MCPServerStatus(nil), r.Data...)
	if data == nil {
		data = []MCPServerStatus{}
	}
	return json.Marshal(struct {
		Data       []MCPServerStatus `json:"data"`
		NextCursor *string           `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: cloneStringPtr(r.NextCursor),
	})
}

type MCPServerOauthLoginParams struct {
	Name               string                            `json:"name,omitempty"`
	ServerName         string                            `json:"serverName,omitempty"`
	ThreadID           *string                           `json:"threadId,omitempty"`
	ClientRegistration *MCPServerOauthClientRegistration `json:"clientRegistration,omitempty"`
	Scopes             []string                          `json:"scopes,omitempty"`
	TimeoutSecs        *uint64                           `json:"timeoutSecs,omitempty"`
}

func (p MCPServerOauthLoginParams) MarshalJSON() ([]byte, error) {
	type payload struct {
		Name               string                            `json:"name"`
		ThreadID           *string                           `json:"threadId,omitempty"`
		ClientRegistration *MCPServerOauthClientRegistration `json:"clientRegistration,omitempty"`
		Scopes             []string                          `json:"scopes,omitempty"`
		TimeoutSecs        *uint64                           `json:"timeoutSecs,omitempty"`
	}
	return json.Marshal(payload{
		Name:               firstNonEmptyMCP(p.Name, p.ServerName),
		ThreadID:           cloneStringPtr(p.ThreadID),
		ClientRegistration: p.ClientRegistration,
		Scopes:             append([]string(nil), p.Scopes...),
		TimeoutSecs:        cloneUint64PtrMCP(p.TimeoutSecs),
	})
}

// MCPServerOauthClientRegistration mirrors Rust
// McpServerOauthClientRegistration (auto|cimd|dcr) for a single OAuth login
// (Rust 6dc3ac8721 / 4c89139da9). Omission preserves automatic discovery.
type MCPServerOauthClientRegistration string

const (
	MCPServerOauthClientRegistrationAuto MCPServerOauthClientRegistration = "auto"
	MCPServerOauthClientRegistrationCimd MCPServerOauthClientRegistration = "cimd"
	MCPServerOauthClientRegistrationDcr  MCPServerOauthClientRegistration = "dcr"
)

func (r MCPServerOauthClientRegistration) Valid() bool {
	switch r {
	case MCPServerOauthClientRegistrationAuto, MCPServerOauthClientRegistrationCimd, MCPServerOauthClientRegistrationDcr:
		return true
	default:
		return false
	}
}

type MCPServerOauthLoginResponse struct {
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	URL              string `json:"url,omitempty"`
	// LoginID identifies this login attempt across the response and the
	// completion notification. Older servers omit the field (Rust #49276).
	LoginID *string `json:"loginId,omitempty"`
}

func (r *MCPServerOauthLoginResponse) MarshalJSON() ([]byte, error) {
	authorizationURL := r.AuthorizationURL
	if authorizationURL == "" {
		authorizationURL = r.URL
	}
	var loginID *string
	if r.LoginID != nil {
		value := *r.LoginID
		loginID = &value
	}
	return json.Marshal(struct {
		AuthorizationURL string  `json:"authorizationUrl"`
		LoginID          *string `json:"loginId,omitempty"`
	}{
		AuthorizationURL: authorizationURL,
		LoginID:          loginID,
	})
}

type MCPServerOauthCancelParams struct {
	Name       string  `json:"name,omitempty"`
	ServerName string  `json:"serverName,omitempty"`
	ThreadID   *string `json:"threadId,omitempty"`
}

func (p MCPServerOauthCancelParams) MarshalJSON() ([]byte, error) {
	type payload struct {
		Name     string  `json:"name"`
		ThreadID *string `json:"threadId,omitempty"`
	}
	return json.Marshal(payload{
		Name:     firstNonEmptyMCP(p.Name, p.ServerName),
		ThreadID: cloneStringPtr(p.ThreadID),
	})
}

type MCPServerOauthCancelResponse struct{}

type MCPServerRefreshResponse struct{}

type MCPResourceReadParams struct {
	ThreadID   *string `json:"threadId,omitempty"`
	Server     string  `json:"server,omitempty"`
	ServerName string  `json:"serverName,omitempty"`
	URI        string  `json:"uri"`
	// OriginCallID scopes the read to the app tool call that produced the
	// widget (Rust #39187): the app, account link, and policy context of the
	// originating call are used when reading the associated resource.
	OriginCallID string `json:"originCallId,omitempty"`
	// ConnectorID restricts hosted app resource reads to the connector that
	// produced them (Rust #39244).
	ConnectorID string `json:"connectorId,omitempty"`
	// Target selects a hosted app and account directly, without tool discovery
	// (Rust #47248). The backend supports it only for `codex_apps`; omitting it
	// keeps the legacy discovery behavior.
	Target *MCPResourceReadTarget `json:"target,omitempty"`
}

// MCPResourceReadTarget is a direct hosted app/account resource target
// (Rust #47248). LinkID is required on the wire but nullable: a string selects
// an account, while null requests no-auth access under the backend's policy.
// LinkIDSet records presence so a missing linkId is rejected instead of being
// treated as an explicit no-auth request.
type MCPResourceReadTarget struct {
	ConnectorID string
	LinkID      *string
	LinkIDSet   bool
}

func (t MCPResourceReadTarget) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ConnectorID string  `json:"connectorId"`
		LinkID      *string `json:"linkId"`
	}{ConnectorID: strings.TrimSpace(t.ConnectorID), LinkID: cloneStringPtr(t.LinkID)})
}

func (t *MCPResourceReadTarget) UnmarshalJSON(data []byte) error {
	if t == nil {
		return errors.New("resource read target is nil")
	}
	var wire struct {
		ConnectorID string          `json:"connectorId"`
		LinkID      json.RawMessage `json:"linkId"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	t.ConnectorID = strings.TrimSpace(wire.ConnectorID)
	t.LinkID = nil
	t.LinkIDSet = false
	if len(wire.LinkID) == 0 {
		return nil
	}
	t.LinkIDSet = true
	if bytes.Equal(bytes.TrimSpace(wire.LinkID), []byte("null")) {
		return nil
	}
	var linkID string
	if err := json.Unmarshal(wire.LinkID, &linkID); err != nil {
		return err
	}
	t.LinkID = &linkID
	return nil
}

func (p MCPResourceReadParams) MarshalJSON() ([]byte, error) {
	type payload struct {
		ThreadID     *string                `json:"threadId,omitempty"`
		Server       string                 `json:"server"`
		URI          string                 `json:"uri"`
		OriginCallID string                 `json:"originCallId,omitempty"`
		ConnectorID  string                 `json:"connectorId,omitempty"`
		Target       *MCPResourceReadTarget `json:"target,omitempty"`
	}
	return json.Marshal(payload{
		ThreadID:     cloneStringPtr(p.ThreadID),
		Server:       firstNonEmptyMCP(p.Server, p.ServerName),
		URI:          p.URI,
		OriginCallID: strings.TrimSpace(p.OriginCallID),
		ConnectorID:  strings.TrimSpace(p.ConnectorID),
		Target:       p.Target,
	})
}

type MCPResourceReadResponse struct {
	Contents []MCPResourceContent `json:"contents"`
	// OriginCallID echoes the scoped origin on successful reads (Rust #39187).
	OriginCallID string `json:"originCallId,omitempty"`
}

func (r *MCPResourceReadResponse) MarshalJSON() ([]byte, error) {
	contents := append([]MCPResourceContent(nil), r.Contents...)
	if contents == nil {
		contents = []MCPResourceContent{}
	}
	return json.Marshal(struct {
		Contents     []MCPResourceContent `json:"contents"`
		OriginCallID string               `json:"originCallId,omitempty"`
	}{Contents: contents, OriginCallID: strings.TrimSpace(r.OriginCallID)})
}

type MCPResourceContent struct {
	URI      string         `json:"uri"`
	MimeType string         `json:"mimeType,omitempty"`
	Text     string         `json:"text,omitempty"`
	Blob     string         `json:"blob,omitempty"`
	Meta     map[string]any `json:"_meta,omitempty"`
}

func (c *MCPResourceContent) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}
	if c.Blob != "" {
		return json.Marshal(struct {
			URI      string         `json:"uri"`
			MimeType string         `json:"mimeType,omitempty"`
			Blob     string         `json:"blob"`
			Meta     map[string]any `json:"_meta,omitempty"`
		}{
			URI:      c.URI,
			MimeType: c.MimeType,
			Blob:     c.Blob,
			Meta:     cloneAnyMap(c.Meta),
		})
	}
	return json.Marshal(struct {
		URI      string         `json:"uri"`
		MimeType string         `json:"mimeType,omitempty"`
		Text     string         `json:"text"`
		Meta     map[string]any `json:"_meta,omitempty"`
	}{
		URI:      c.URI,
		MimeType: c.MimeType,
		Text:     c.Text,
		Meta:     cloneAnyMap(c.Meta),
	})
}

type MCPToolCallParams struct {
	// Context carries the caller's trace context through the MCP transport and
	// any nested server-request responses (Rust #47212).
	Context    context.Context `json:"-"`
	ThreadID   string          `json:"threadId,omitempty"`
	TurnID     string          `json:"turnId,omitempty"`
	ItemID     string          `json:"itemId,omitempty"`
	Server     string          `json:"server,omitempty"`
	Tool       string          `json:"tool,omitempty"`
	ServerName string          `json:"serverName,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Arguments  any             `json:"arguments,omitempty"`
	Meta       any             `json:"_meta,omitempty"`
	CallID     string          `json:"-"`

	// SessionID, WindowID and OriginItemID feed the request `_meta` only (Rust
	// with_mcp_tool_call_ids_meta, #40866/#45409); the tools/call wire params
	// carry name, arguments and `_meta`, so they are never serialized on their
	// own. SessionID is the identity shared by the root thread and its
	// descendants, WindowID the caller's `{thread_id}:{window_number}` window,
	// and OriginItemID the Responses item that requested the call.
	SessionID    string `json:"-"`
	WindowID     string `json:"-"`
	OriginItemID string `json:"-"`

	// PermissionProfile is the opaque permission-profile document forwarded to
	// `codex sandbox --sandbox-state-json` (Rust SandboxState::permission_profile).
	// It is JSON text, not a profile label, so consumers can preserve unknown
	// fields instead of rebuilding the policy (#53709).
	PermissionProfile    string `json:"-"`
	SandboxCWD           string `json:"-"`
	CodexLinuxSandboxExe string `json:"-"`
	UseLegacyLandlock    bool   `json:"-"`
	// UseMXC carries the environment's effective Windows sandbox backend
	// (Rust #49781's `use_mxc`).
	UseMXC bool `json:"-"`
	// SandboxLauncherExecutable is the verified local CLI advertised under the
	// reserved launcher key (Rust #52182). Empty omits the launcher envelope and
	// never authorizes a PATH lookup.
	SandboxLauncherExecutable string `json:"-"`
	ServerEnvironmentID       string `json:"-"`
	// SupportsSandboxStateMeta reports the negotiated sandbox-state capability:
	// the server advertised `codex/sandbox-state` or its deprecated `-meta`
	// alias (#53709).
	SupportsSandboxStateMeta bool `json:"-"`
}

func (p MCPToolCallParams) MarshalJSON() ([]byte, error) {
	type payload struct {
		ThreadID  string `json:"threadId"`
		TurnID    string `json:"turnId,omitempty"`
		ItemID    string `json:"itemId,omitempty"`
		Server    string `json:"server"`
		Tool      string `json:"tool"`
		Arguments any    `json:"arguments,omitempty"`
		Meta      any    `json:"_meta,omitempty"`
	}
	return json.Marshal(payload{
		ThreadID:  firstNonEmptyMCP(p.ThreadID),
		TurnID:    firstNonEmptyMCP(p.TurnID),
		ItemID:    firstNonEmptyMCP(p.ItemID),
		Server:    firstNonEmptyMCP(p.Server, p.ServerName),
		Tool:      firstNonEmptyMCP(p.Tool, p.ToolName),
		Arguments: cloneJSONValue(p.Arguments),
		Meta:      cloneJSONValue(p.Meta),
	})
}

type MCPToolCallResponse struct {
	Content           []MCPToolCallContent `json:"content"`
	StructuredContent any                  `json:"structuredContent,omitempty"`
	IsError           *bool                `json:"isError,omitempty"`
	Meta              any                  `json:"_meta,omitempty"`
}

func (r *MCPToolCallResponse) MarshalJSON() ([]byte, error) {
	content := append([]MCPToolCallContent(nil), r.Content...)
	if content == nil {
		content = []MCPToolCallContent{}
	}
	return json.Marshal(struct {
		Content           []MCPToolCallContent `json:"content"`
		StructuredContent any                  `json:"structuredContent,omitempty"`
		IsError           *bool                `json:"isError,omitempty"`
		Meta              any                  `json:"_meta,omitempty"`
	}{
		Content:           content,
		StructuredContent: r.StructuredContent,
		IsError:           r.IsError,
		Meta:              r.Meta,
	})
}

type MCPToolCallContent struct {
	Type string         `json:"type"`
	Text string         `json:"text,omitempty"`
	Raw  map[string]any `json:"-"`
}

func (c *MCPToolCallContent) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Raw = cloneAnyMap(raw)
	c.Type = stringFromAnyMap(raw, "type")
	c.Text = stringFromAnyMap(raw, "text")
	return nil
}

func (c MCPToolCallContent) MarshalJSON() ([]byte, error) {
	out := cloneAnyMap(c.Raw)
	if out == nil {
		out = map[string]any{}
	}
	if strings.TrimSpace(c.Type) != "" {
		out["type"] = c.Type
	}
	if c.Text != "" {
		out["text"] = c.Text
	}
	return json.Marshal(out)
}

func (c *MCPToolCallContent) Map() map[string]any {
	if c == nil {
		return map[string]any{}
	}
	out := cloneAnyMap(c.Raw)
	if out == nil {
		out = map[string]any{}
	}
	if strings.TrimSpace(c.Type) != "" {
		out["type"] = c.Type
	}
	if c.Text != "" {
		out["text"] = c.Text
	}
	return out
}

type MCPService struct {
	mu      sync.Mutex
	servers map[string]MCPServerStatus
	configs map[string]ServerConfig
	// disabledConfigs retains disabled registrations so login and diagnostics can
	// report why they are unavailable (Rust #56266).
	disabledConfigs map[string]ServerConfig
	dynamicConfig   map[string]bool
	required        map[string]bool
	starting        map[string]int
	httpClients     map[string]*cachedMCPHTTPClient
	stdioClients    map[string]*cachedMCPStdioClient
	// discoverLocalStdio mirrors Rust McpConfig::discover_local_stdio: it gates
	// both local stdio protocol discovery (#57210) and connection sharing
	// (#57211).
	discoverLocalStdio bool
	// networkPolicyMetadata, when set, supplies call-scoped network policy
	// metadata to trusted ChatGPT MCP servers (Rust
	// NetworkPolicyMetadataProvider, #56278). A nil provider disables the
	// capability advertisement.
	networkPolicyMetadata NetworkPolicyMetadataProvider
	// chatGPTBaseURL is the configured ChatGPT origin used to decide whether a
	// staging MCP server is trusted for policy metadata.
	chatGPTBaseURL string
	oauthLogins    map[string]*OAuthLoginServer
	oauth          *OAuthStore
	resourceCache  *MCPResourceCache
	elicitation    MCPElicitationHandler
	progress       MCPProgressHandler
	roots          MCPRootsProvider
	oauthComplete  MCPOAuthLoginCompletionHandler
	invocations    *MCPInvocationRegistry
	// oauthCompleteListeners let multiple in-process consumers observe the same
	// login completion while the app-server handler remains installed.
	oauthCompleteListeners map[uint64]MCPOAuthLoginCompletionHandlerFunc
	nextOAuthListenerID    uint64
	openAIForm             bool
	userVerification       bool
	generation             uint64
	// resourceGeneration advances when resource catalogs and contents must be
	// refreshed even if the connection set is reused (Rust #46033).
	resourceGeneration uint64
	// sharedDefinitions shares immutable normalized tool definitions within this
	// manager without keeping completed sessions alive (Rust #55724).
	sharedDefinitions *sharedMCPDefinitions
	// authChangeSource, when set, lets opted-in stdio servers receive auth
	// change notifications (Rust #43428).
	authChangeSource    MCPAuthChangeSource
	sharedHTTPClient    HTTPDoer
	sharedHTTPClientKey string
	// permissionProfile / serverPermissionProfiles are the authority published
	// with this runtime (Rust #40728).
	permissionProfile        *sandbox.PermissionProfile
	serverPermissionProfiles map[string]*sandbox.PermissionProfile
	// codexSelfExe is the host-supplied re-exec binary advertised as a sandbox
	// launcher after a capability probe (Rust McpConfig::codex_self_exe).
	codexSelfExe string
	// codexLinuxSandboxExe, useLegacyLandlock, environmentCWDs and
	// environmentUseMXC mirror the McpConfig fields of the same names that the
	// sandbox-state metadata reports (#49781/#52182).
	codexLinuxSandboxExe string
	useLegacyLandlock    bool
	environmentCWDs      map[string]string
	environmentUseMXC    map[string]bool
	// selectedEnvironments is the ordered executor selection published with this
	// runtime (Rust McpServerContributionContext::selected_environments, #51503).
	// A nil value means no selection was captured, and a non-nil empty value
	// means the thread explicitly selected no environments.
	selectedEnvironments *SelectedEnvironments
	// TrustedAccess, when set, attaches host-owned openai/entitlementContext
	// metadata to eligible plugin MCP calls (Rust TrustedAccessContext,
	// #40992/#41005).
	TrustedAccess *TrustedAccessContext
	// appsMCPProductSKU is the runtime's `apps_mcp_product_sku`, used to bound
	// product SKU attribution on raw catalog size telemetry (Rust #51215).
	appsMCPProductSKU string
	// refreshToolListOnNotifications mirrors Rust McpConfig::mcp_tool_list_changed
	// (feature mcp_tool_list_changed, disabled by default) for ordinary MCP
	// servers (Rust #54656).
	refreshToolListOnNotifications bool
	// toolListRefresh holds each server's notification-refresh bookkeeping
	// (Rust #54656). Guarded by mu.
	toolListRefresh map[string]*mcpToolListRefreshState
}

// SetTrustedAccess installs the trusted-access context used to attach
// host-owned openai/entitlementContext metadata to eligible plugin MCP calls
// (Rust #40992/#41005). It is a no-op when the service is nil.
func (s *MCPService) SetTrustedAccess(context *TrustedAccessContext) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TrustedAccess = context
}

var sharedOptionalMCPStartupGrace = struct {
	sync.Mutex
	deadlines map[string]time.Time
}{deadlines: map[string]time.Time{}}

type cachedMCPHTTPClient struct {
	key            string
	client         *httpClient
	startupTimeout time.Duration
}

type cachedMCPStdioClient struct {
	key            string
	client         *stdioClient
	startupTimeout time.Duration
	// release returns a shared connection to the process-wide pool (Rust
	// #57211). It is nil for private connections, which Close owns.
	release func()
	// lease is this chat's server-side lease handle on a shared connection. It
	// stays closed unless the helper negotiated codex/connection-lease (Rust
	// #57978).
	lease *mcpChatLease
}

func NewMCPService(runtime *RuntimeConfig) *MCPService {
	service := &MCPService{servers: map[string]MCPServerStatus{}, configs: map[string]ServerConfig{}, disabledConfigs: map[string]ServerConfig{}, dynamicConfig: map[string]bool{}, required: map[string]bool{}, starting: map[string]int{}, httpClients: map[string]*cachedMCPHTTPClient{}, stdioClients: map[string]*cachedMCPStdioClient{}, oauthLogins: map[string]*OAuthLoginServer{}, resourceCache: NewMCPResourceCache(nil), generation: 1, appsMCPProductSKU: DefaultCodexAppsMCPProductSKU, invocations: NewMCPInvocationRegistry(), sharedDefinitions: newSharedMCPDefinitions()}
	if runtime != nil {
		// Rust reads `config.apps_mcp_product_sku.as_deref().unwrap_or(..)`; Go's
		// runtime config cannot distinguish an absent SKU from an empty one, so
		// an empty value keeps the "codex" default.
		if sku := strings.TrimSpace(runtime.AppsMCPProductSKU); sku != "" {
			service.appsMCPProductSKU = sku
		}
		service.selectedEnvironments = runtime.SelectedEnvironments.Clone()
		service.sharedHTTPClient = runtime.HTTPClient
		service.codexSelfExe = strings.TrimSpace(runtime.CodexSelfExe)
		if service.codexSelfExe == "" {
			// Rust's arg0 layer sets `config.codex_self_exe` from
			// `std::env::current_exe()`; the MCP runtime inherits the same host
			// binary unless a caller supplies a dispatch-path override.
			if exe, err := os.Executable(); err == nil {
				service.codexSelfExe = strings.TrimSpace(exe)
			}
		}
		service.codexLinuxSandboxExe = strings.TrimSpace(runtime.CodexLinuxSandboxExe)
		service.useLegacyLandlock = runtime.UseLegacyLandlock
		service.environmentCWDs = cloneStringMap(runtime.EnvironmentCWDs)
		service.environmentUseMXC = cloneBoolMap(runtime.EnvironmentUseMXC)
		service.sharedHTTPClientKey = mcpHTTPDoerIdentity(runtime.HTTPClient)
		service.refreshToolListOnNotifications = runtime.RefreshToolListOnNotifications
		service.discoverLocalStdio = runtime.DiscoverLocalStdio
		service.networkPolicyMetadata = runtime.NetworkPolicyMetadata
		service.chatGPTBaseURL = strings.TrimSpace(runtime.ChatGPTBaseURL)
		if strings.TrimSpace(runtime.CodexHome) != "" {
			service.oauth = NewOAuthStoreWithMode(runtime.CodexHome, runtime.OAuthCredentialsStoreMode)
		}
		for name, registration := range runtime.Servers {
			name = strings.TrimSpace(name)
			if name == "" {
				name = strings.TrimSpace(registration.Name)
			}
			if name == "" {
				continue
			}
			info := MCPServerInfo{Name: name, Command: firstNonEmptyMCP(registration.Config.Command, registration.Config.URL), Args: append([]string(nil), registration.Config.Args...)}
			config := cloneServerConfig(&registration.Config)
			config.ProtocolMode = effectiveMCPProtocolMode(runtime, name, registration)
			// Rust #57210: with mcp_connection_sharing enabled, an eligible local
			// stdio server probes its protocol before falling back to the legacy
			// handshake. An explicit registration override stays authoritative.
			if runtime.DiscoverLocalStdio && config.ProtocolModeOverride == nil &&
				mcpServerCanDiscoverLocalStdio(name, &config) {
				config.ProtocolMode = MCPProtocolDiscoverLocalStdio
			}
			// Rust applies the runtime's read-only policy to every enabled
			// server (McpConnectionSet::with_read_only_mcp_tools).
			config.RequiresReadOnlyTools = runtime.RequiresReadOnlyMCPTools
			// Rust #48143: the registration's credential authority survives into
			// the effective server, so an executor-discovered declaration cannot
			// resolve host environment credentials after a reconnect.
			config.CredentialPolicy = EffectiveCredentialPolicy(registration.CredentialPolicy)
			if strings.TrimSpace(config.OAuthServerName) == "" {
				config.OAuthServerName = name
			}
			if strings.TrimSpace(config.CodexHome) == "" {
				config.CodexHome = runtime.CodexHome
			}
			if registration.Config.Required {
				service.required[name] = true
			}
			if !registration.Config.Enabled {
				service.disabledConfigs[name] = config
				continue
			}
			if !mcpRegistrationEnvironmentAvailable(name, registration, runtime) {
				continue
			}
			service.configs[name] = config
			authStatus := service.authStatusForConfig(name, &config)
			var pluginID *string
			if strings.TrimSpace(registration.PluginID) != "" {
				pluginID = cloneStringPtr(&registration.PluginID)
			}
			service.servers[name] = MCPServerStatus{
				Name:           name,
				PluginID:       pluginID,
				pluginHostRoot: strings.TrimSpace(registration.PluginHostRoot),
				Server:         info,
				ServerInfo:     &info,
				State:          MCPServerReady,
				AuthStatus:     authStatus,
				source:         SourceFromRegistration(&registration),
				hostOwnedApps:  isHostOwnedCodexAppsRegistration(name, registration),
			}
		}
		service.permissionProfile = cloneMCPPermissionProfile(runtime.PermissionProfile)
		// A non-nil map means the runtime published authority; servers absent
		// from it have none and their calls are rejected (Rust #40728).
		if runtime.ServerPermissionProfiles != nil {
			service.serverPermissionProfiles = make(map[string]*sandbox.PermissionProfile, len(runtime.ServerPermissionProfiles))
			for name, profile := range runtime.ServerPermissionProfiles {
				service.serverPermissionProfiles[strings.TrimSpace(name)] = cloneMCPPermissionProfile(profile)
			}
		}
	}
	return service
}

// mcpServerEnvironmentAvailable mirrors Rust #39335: attachment-scoped MCP
// servers (those bound to a non-local environment) stay disabled while their
// environment configuration is pending, failed, or unselected. The controller-
// owned Apps server and selected-plugin access are preserved through
// AvailableEnvironment entries supplied by the runtime.
func mcpServerEnvironmentAvailable(config *ServerConfig, available []string) bool {
	if config == nil {
		return false
	}
	environmentID := strings.TrimSpace(config.EnvironmentID)
	if environmentID == "" {
		environmentID = DefaultMCPServerEnvironmentID
	}
	if environmentID == DefaultMCPServerEnvironmentID {
		return true
	}
	// No thread environment selection means the runtime did not attach any
	// environment; attachment-scoped filtering only applies once selections
	// exist, so executor-owned and pre-attachment servers remain usable.
	if len(available) == 0 {
		return true
	}
	for _, selected := range available {
		if strings.TrimSpace(selected) == environmentID {
			return true
		}
	}
	return false
}

func (s *MCPService) ApplyRuntimeConfig(runtime *RuntimeConfig) {
	if s == nil {
		return
	}
	refreshed := NewMCPService(runtime)
	s.mu.Lock()
	nextHTTPClients := map[string]*cachedMCPHTTPClient{}
	nextStdioClients := map[string]*cachedMCPStdioClient{}
	var oldHTTPClients []*httpClient
	var oldStdioClients []*cachedMCPStdioClient
	for name, cached := range s.httpClients {
		config, ok := refreshed.configs[name]
		sharedHTTPClientKey := refreshed.sharedHTTPClientKey
		if ok && config.HTTPClient != nil {
			sharedHTTPClientKey = mcpHTTPDoerIdentity(config.HTTPClient)
		}
		if ok && cached != nil && cached.client != nil && !cached.client.isClosed() &&
			cached.key == refreshed.mcpHTTPClientCacheKey(&config, s.openAIForm, mcpServerAllowsUserVerification(refreshed.servers, name, s.userVerification), sharedHTTPClientKey) {
			nextHTTPClients[name] = cached
			preserveMCPServerInventory(refreshed.servers, s.servers, name)
			continue
		}
		if cached != nil && cached.client != nil {
			oldHTTPClients = append(oldHTTPClients, cached.client)
		}
	}
	for name, cached := range s.stdioClients {
		config, ok := refreshed.configs[name]
		if ok && cached != nil && cached.client != nil && !cached.client.isClosed() &&
			cached.key == refreshed.mcpStdioClientCacheKey(&config, s.openAIForm, mcpServerAllowsUserVerification(refreshed.servers, name, s.userVerification)) {
			nextStdioClients[name] = cached
			preserveMCPServerInventory(refreshed.servers, s.servers, name)
			continue
		}
		if cached != nil && cached.client != nil {
			oldStdioClients = append(oldStdioClients, cached)
		}
	}
	oldOAuthLogins := s.oauthLoginsForCancelLocked()
	s.servers = refreshed.servers
	s.configs = refreshed.configs
	s.disabledConfigs = refreshed.disabledConfigs
	s.dynamicConfig = refreshed.dynamicConfig
	s.required = refreshed.required
	s.starting = refreshed.starting
	s.httpClients = nextHTTPClients
	s.stdioClients = nextStdioClients
	s.oauthLogins = map[string]*OAuthLoginServer{}
	s.oauth = refreshed.oauth
	s.sharedHTTPClient = refreshed.sharedHTTPClient
	s.sharedHTTPClientKey = refreshed.sharedHTTPClientKey
	s.permissionProfile = refreshed.permissionProfile
	s.serverPermissionProfiles = refreshed.serverPermissionProfiles
	s.selectedEnvironments = refreshed.selectedEnvironments
	s.discoverLocalStdio = refreshed.discoverLocalStdio
	s.networkPolicyMetadata = refreshed.networkPolicyMetadata
	s.chatGPTBaseURL = refreshed.chatGPTBaseURL
	s.refreshToolListOnNotifications = refreshed.refreshToolListOnNotifications
	s.generation++
	if s.resourceCache == nil {
		s.resourceCache = NewMCPResourceCache(nil)
	}
	s.mu.Unlock()
	closeHTTPClients(oldHTTPClients)
	retireCachedStdioClients(oldStdioClients)
	cancelOAuthLogins(oldOAuthLogins)
	s.clearResourceCache()
}

func preserveMCPServerInventory(next map[string]MCPServerStatus, previous map[string]MCPServerStatus, name string) {
	current, ok := next[name]
	if !ok {
		return
	}
	old, ok := previous[name]
	if !ok {
		return
	}
	current.State = old.State
	current.Error = cloneStringPtr(old.Error)
	// The reused connection keeps its already-materialized catalog, which the
	// raw size telemetry reports as the cached source (Rust #51215).
	current.catalogSource = BindingCatalogSourceCached
	current.Tools = append([]MCPToolInfo(nil), old.Tools...)
	current.Resources = append([]MCPResource(nil), old.Resources...)
	current.ResourceTemplates = append([]MCPResourceTemplate(nil), old.ResourceTemplates...)
	next[name] = current
}

// SelectedEnvironments returns the ordered executor selection captured for this
// runtime, mirroring Rust's `McpServerContributionContext::selected_environments`
// (#51503): priority order and pending or failed entries are preserved. A nil
// result means no selection was captured (threadless discovery); a non-nil empty
// result means the thread explicitly selected no environments.
func (s *MCPService) SelectedEnvironments() *SelectedEnvironments {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedEnvironments.Clone()
}

func (s *MCPService) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	httpClients := s.httpClientsForCloseLocked()
	stdioClients := s.stdioClientsForCloseLocked()
	oauthLogins := s.oauthLoginsForCancelLocked()
	s.httpClients = map[string]*cachedMCPHTTPClient{}
	s.stdioClients = map[string]*cachedMCPStdioClient{}
	s.oauthLogins = map[string]*OAuthLoginServer{}
	s.mu.Unlock()
	closeHTTPClients(httpClients)
	closeCachedStdioClients(stdioClients)
	cancelOAuthLogins(oauthLogins)
	s.clearResourceCache()
	return nil
}

func (s *MCPService) SetServer(status MCPServerStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := status.effectiveName()
	if name == "" {
		return
	}
	status.Name = name
	s.servers[name] = cloneMCPServerStatus(status)
	s.clearResourceCache()
}

func (s *MCPService) SetServerConfig(name string, config *ServerConfig) {
	s.mu.Lock()
	name = strings.TrimSpace(name)
	if name == "" {
		s.mu.Unlock()
		return
	}
	s.generation++
	if s.configs == nil {
		s.configs = map[string]ServerConfig{}
	}
	if s.dynamicConfig == nil {
		s.dynamicConfig = map[string]bool{}
	}
	if s.required == nil {
		s.required = map[string]bool{}
	}
	oldHTTPClient := s.deleteHTTPClientLocked(name)
	oldStdioClient := s.deleteStdioClientLocked(name)
	oldOAuthLogin := s.deleteOAuthLoginLocked(name)
	if config == nil {
		delete(s.configs, name)
		delete(s.disabledConfigs, name)
		delete(s.dynamicConfig, name)
		delete(s.required, name)
		delete(s.servers, name)
		s.clearResourceCache()
		s.mu.Unlock()
		closeHTTPClients([]*httpClient{oldHTTPClient})
		retireCachedStdioClients([]*cachedMCPStdioClient{oldStdioClient})
		cancelOAuthLogins([]*OAuthLoginServer{oldOAuthLogin})
		return
	}
	if !config.Enabled {
		delete(s.configs, name)
		if s.disabledConfigs == nil {
			s.disabledConfigs = map[string]ServerConfig{}
		}
		s.disabledConfigs[name] = cloneServerConfig(config)
		delete(s.dynamicConfig, name)
		if config.Required {
			s.required[name] = true
		} else {
			delete(s.required, name)
		}
		delete(s.servers, name)
		s.clearResourceCache()
		s.mu.Unlock()
		closeHTTPClients([]*httpClient{oldHTTPClient})
		retireCachedStdioClients([]*cachedMCPStdioClient{oldStdioClient})
		cancelOAuthLogins([]*OAuthLoginServer{oldOAuthLogin})
		return
	}
	delete(s.disabledConfigs, name)
	cloned := cloneServerConfig(config)
	if strings.TrimSpace(cloned.OAuthServerName) == "" {
		cloned.OAuthServerName = name
	}
	if strings.TrimSpace(cloned.CodexHome) == "" && s.oauth != nil {
		cloned.CodexHome = s.oauth.CodexHome
	}
	if s.servers == nil {
		s.servers = map[string]MCPServerStatus{}
	}
	s.configs[name] = cloned
	s.dynamicConfig[name] = true
	if cloned.Required {
		s.required[name] = true
	} else {
		delete(s.required, name)
	}
	info := MCPServerInfo{Name: name, Command: firstNonEmptyMCP(cloned.Command, cloned.URL), Args: append([]string(nil), cloned.Args...)}
	s.servers[name] = MCPServerStatus{
		Name:       name,
		Server:     info,
		ServerInfo: &info,
		State:      MCPServerReady,
		AuthStatus: s.authStatusForConfig(name, &cloned),
	}
	s.clearResourceCache()
	s.mu.Unlock()
	closeHTTPClients([]*httpClient{oldHTTPClient})
	retireCachedStdioClients([]*cachedMCPStdioClient{oldStdioClient})
	cancelOAuthLogins([]*OAuthLoginServer{oldOAuthLogin})
}

// ConfiguredToolOutputLimit returns the configured per-tool output token limit
// for a server's tool, or nil when unset (Rust #41421). The effective limit is
// resolved from the server config so the MCP executor can truncate a tool
// result before it reaches the model.
func (s *MCPService) ConfiguredToolOutputLimit(serverName, toolName string) *int {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	config, ok := s.configs[strings.TrimSpace(serverName)]
	if !ok {
		return nil
	}
	toolConfig, ok := config.Tools[strings.TrimSpace(toolName)]
	if !ok || toolConfig.OutputTokenLimit == nil || *toolConfig.OutputTokenLimit <= 0 {
		return nil
	}
	limit := *toolConfig.OutputTokenLimit
	return &limit
}

func (s *MCPService) SetElicitationHandler(handler MCPElicitationHandler) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.elicitation = handler
}

func (s *MCPService) SetProgressHandler(handler MCPProgressHandler) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = handler
}

func (s *MCPService) SetRootsProvider(provider MCPRootsProvider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roots = provider
}

func (s *MCPService) SetOAuthLoginCompletionHandler(handler MCPOAuthLoginCompletionHandler) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauthComplete = handler
}

// AddOAuthLoginCompletionListener registers an additional completion listener.
// The returned function unsubscribes it. The app-server handler continues to
// receive the same completion.
func (s *MCPService) AddOAuthLoginCompletionListener(listener MCPOAuthLoginCompletionHandlerFunc) func() {
	if s == nil || listener == nil {
		return func() {}
	}
	s.mu.Lock()
	if s.oauthCompleteListeners == nil {
		s.oauthCompleteListeners = map[uint64]MCPOAuthLoginCompletionHandlerFunc{}
	}
	id := s.nextOAuthListenerID
	s.nextOAuthListenerID++
	s.oauthCompleteListeners[id] = listener
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.oauthCompleteListeners, id)
		s.mu.Unlock()
	}
}

func (s *MCPService) SetOpenAIFormElicitationEnabled(enabled bool) {
	s.setElicitationCapabilities(&enabled, nil)
}

// SetUserVerificationEnabled controls the native user-verification extension
// independently of the legacy OpenAI form capability. App-server activates it
// only for trusted local clients (Rust #44613).
func (s *MCPService) SetUserVerificationEnabled(enabled bool) {
	s.setElicitationCapabilities(nil, &enabled)
}

func (s *MCPService) setElicitationCapabilities(openAIForm *bool, userVerification *bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	changed := false
	if openAIForm != nil && s.openAIForm != *openAIForm {
		s.openAIForm = *openAIForm
		changed = true
	}
	if userVerification != nil && s.userVerification != *userVerification {
		s.userVerification = *userVerification
		changed = true
	}
	if !changed {
		s.mu.Unlock()
		return
	}
	s.generation++
	oldHTTPClients := s.httpClientsForCloseLocked()
	oldStdioClients := s.stdioClientsForCloseLocked()
	s.httpClients = map[string]*cachedMCPHTTPClient{}
	s.stdioClients = map[string]*cachedMCPStdioClient{}
	s.mu.Unlock()
	closeHTTPClients(oldHTTPClients)
	retireCachedStdioClients(oldStdioClients)
	s.clearResourceCache()
}

func (s *MCPService) ListStatus(params *MCPListServerStatusParams) *MCPListServerStatusResponse {
	response, _ := s.ListStatusChecked(params)
	return response
}

func (s *MCPService) ConfiguredStatuses() []MCPServerStatus {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	statuses := make([]MCPServerStatus, 0, len(s.servers))
	configs := make(map[string]ServerConfig, len(s.configs))
	for name, config := range s.configs {
		configs[name] = cloneServerConfig(&config)
	}
	for _, status := range s.servers {
		cloned := cloneMCPServerStatus(status)
		if config, ok := configs[cloned.effectiveName()]; ok {
			cloned.AuthStatus = s.authStatusForConfig(cloned.effectiveName(), &config)
		}
		statuses = append(statuses, cloned)
	}
	s.mu.Unlock()
	sort.SliceStable(statuses, func(i int, j int) bool {
		return statuses[i].effectiveName() < statuses[j].effectiveName()
	})
	return statuses
}

// HasServers reports whether the service has at least one configured MCP
// server, mirroring Rust's McpRuntime::has_servers so core tool planning can
// gate MCP resource tools on configured servers rather than on tool search.
func (s *MCPService) HasServers() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.configs) > 0
}

func (s *MCPService) ListStatusChecked(params *MCPListServerStatusParams) (*MCPListServerStatusResponse, error) {
	return s.ListStatusCheckedWithObserver(params, nil)
}

// ValidateRequiredServers waits for the current required MCP servers to finish
// their startup inventory and reports all failures together. This mirrors the
// Rust session initialization gate: optional server failures remain
// best-effort, while a required server prevents a new session from loading.
func (s *MCPService) ValidateRequiredServers(threadID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	required := make([]string, 0, len(s.required))
	for name, enabled := range s.required {
		if enabled {
			required = append(required, name)
		}
	}
	s.mu.Unlock()
	if len(required) == 0 {
		return nil
	}
	sort.Strings(required)

	threadID = strings.TrimSpace(threadID)
	var threadIDPtr *string
	if threadID != "" {
		threadIDPtr = &threadID
	}
	if _, err := s.ListStatusChecked(&MCPListServerStatusParams{
		ThreadID: threadIDPtr,
		Detail:   &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}); err != nil {
		return err
	}

	s.mu.Lock()
	failures := make([]string, 0, len(required))
	for _, name := range required {
		config, configOK := s.configs[name]
		if !configOK || !config.Enabled {
			failures = append(failures, fmt.Sprintf("%s: required MCP server `%s` was not initialized", name, name))
			continue
		}
		status, statusOK := s.servers[name]
		if !statusOK {
			failures = append(failures, fmt.Sprintf("%s: required MCP server `%s` was not initialized", name, name))
			continue
		}
		switch status.State {
		case MCPServerFailed, MCPServerCancelled, MCPServerStopped:
			reason := ""
			if status.Error != nil {
				reason = strings.TrimSpace(*status.Error)
			}
			if reason == "" {
				reason = string(status.State)
			}
			failures = append(failures, fmt.Sprintf("%s: %s", name, reason))
		}
	}
	s.mu.Unlock()
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("required MCP servers failed to initialize: %s", strings.Join(failures, "; "))
}

// WaitForServerStartup refreshes only the MCP server that owns the selected
// tool. The inventory request can block without occupying the tool execution
// gate, so unrelated calls in the same model response remain runnable.
func (s *MCPService) WaitForServerStartup(ctx context.Context, name string, threadID string) error {
	if s == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	config, ok := s.configs[name]
	status := cloneMCPServerStatus(s.servers[name])
	starting := s.starting[name] > 0
	s.mu.Unlock()
	if !ok || !config.Enabled {
		return nil
	}
	if !starting && (status.State == "" || status.State == MCPServerReady) {
		return nil
	}
	done := make(chan struct{}, 1)
	go func() {
		_ = s.inventoryStatusForConfig(0, name, &config, status, false, strings.TrimSpace(threadID))
		done <- struct{}{}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *MCPService) ListStatusCheckedWithObserver(params *MCPListServerStatusParams, observer MCPStartupObserver) (*MCPListServerStatusResponse, error) {
	var detail *MCPServerStatusDetail
	if params != nil {
		detail = params.Detail
	}
	threadID := ""
	if params != nil && params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
	}
	s.mu.Lock()
	servers := make([]MCPServerStatus, 0, len(s.servers))
	configs := make(map[string]ServerConfig, len(s.configs))
	dynamicConfigs := make(map[string]bool, len(s.dynamicConfig))
	for name, config := range s.configs {
		configs[name] = cloneServerConfig(&config)
	}
	for name, dynamic := range s.dynamicConfig {
		dynamicConfigs[name] = dynamic
	}
	for _, status := range s.servers {
		cloned := cloneMCPServerStatus(status)
		// The catalog source is per call: the stored status keeps the source its catalog
		// was last obtained with, but only a read that touches the catalog this time
		// reports one (Rust #51215 emits the record where the catalog is materialized,
		// not on every later status read), so a read that materializes nothing records
		// no source and no event.
		cloned.catalogSource = ""
		if config, ok := configs[cloned.effectiveName()]; ok {
			cloned.AuthStatus = s.authStatusForConfig(cloned.effectiveName(), &config)
		}
		if !detail.includesTools() {
			cloned.Tools = nil
		}
		if !detail.includesInventory() {
			cloned.Resources = nil
			cloned.ResourceTemplates = nil
		}
		if threadID != "" {
			cloned.RuntimeStatus = s.runtimeConnectionStatus(cloned.effectiveName(), s.starting[cloned.effectiveName()] > 0)
		}
		servers = append(servers, cloned)
	}
	s.mu.Unlock()
	// Rust #48783: an unknown server name yields an empty page, and a known one
	// limits both discovery and the returned page to that server.
	if selected := mcpServerNameFilter(params); selected != "" {
		filtered := make([]MCPServerStatus, 0, 1)
		for _, status := range servers {
			if status.effectiveName() == selected {
				filtered = append(filtered, status)
			}
		}
		servers = filtered
	}
	servers = s.populateStatusInventories(params, detail, observer, servers, configs, dynamicConfigs, threadID)
	sort.SliceStable(servers, func(i int, j int) bool {
		return servers[i].effectiveName() < servers[j].effectiveName()
	})
	page, nextCursor, err := paginateMCPStatuses(servers, params)
	if err != nil {
		return nil, err
	}
	return &MCPListServerStatusResponse{Data: page, NextCursor: nextCursor, Servers: page}, nil
}

// runtimeConnectionStatus derives the thread-runtime connection state for a
// server (Rust #40068). It observes the already-published connection state
// without starting or reconnecting the server. The caller must hold s.mu.
func (s *MCPService) runtimeConnectionStatus(name string, starting bool) *MCPConnectionStatus {
	name = strings.TrimSpace(name)
	status := s.servers[name]
	if !starting {
		switch status.State {
		case MCPServerStarting:
			starting = true
		case MCPServerFailed:
			if status.FailureReason != nil && *status.FailureReason == mcpFailureReasonReauthenticationRequired {
				v := MCPConnectionAuthenticationRequired
				return &v
			}
			v := MCPConnectionFailed
			return &v
		case MCPServerCancelled:
			v := MCPConnectionCancelled
			return &v
		case MCPServerStopped:
			v := MCPConnectionDisabled
			return &v
		case MCPServerReady, "":
			// A registered server that is part of the live connection set and
			// is not mid-startup is considered connected. A previously
			// failed/cancelled server that has transitioned back to ready is
			// connected too.
			v := MCPConnectionConnected
			return &v
		}
	}
	if starting {
		v := MCPConnectionStarting
		return &v
	}
	// A configured server that has not begun startup reports notStarted.
	v := MCPConnectionNotStarted
	return &v
}

type mcpInventoryStatusResult struct {
	Index  int
	Status MCPServerStatus
	Err    error
}

// mcpToolListRefreshApplies mirrors Rust's notification-refresh gate: the
// refresh covers ordinary MCP servers only, and only when the
// `mcp_tool_list_changed` feature is enabled (Rust #54656). The host-owned Codex
// Apps registration keeps its shared provider.
func (s *MCPService) mcpToolListRefreshApplies(name string) bool {
	if s == nil || !s.refreshToolListOnNotifications {
		return false
	}
	return !IsCodexAppsMCPServerName(name)
}

// toolListRefreshStateFor returns the per-server notification-refresh
// bookkeeping, creating it on first use.
func (s *MCPService) toolListRefreshStateFor(name string) *mcpToolListRefreshState {
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolListRefresh == nil {
		s.toolListRefresh = map[string]*mcpToolListRefreshState{}
	}
	state := s.toolListRefresh[name]
	if state == nil {
		state = &mcpToolListRefreshState{}
		s.toolListRefresh[name] = state
	}
	return state
}

// cachedToolListRevision reports the tool-list invalidation revision of the
// connection this service already established for a server. A server without a
// live connection cannot have reported a change, so its revision is zero.
func (s *MCPService) cachedToolListRevision(name string) uint64 {
	if s == nil {
		return 0
	}
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	var revision uint64
	if cached := s.httpClients[name]; cached != nil && cached.client != nil {
		revision = cached.client.toolsListRevision()
	}
	if cached := s.stdioClients[name]; cached != nil && cached.client != nil {
		if stdioRevision := cached.client.toolsListRevision(); stdioRevision > revision {
			revision = stdioRevision
		}
	}
	return revision
}

// retainedToolCatalogUsable reports whether a status read may answer from the
// retained tool catalog. Rust #54656 refuses the cached catalog while the
// connection has an unconsumed tools/list_changed notification, so the read
// falls through to a fresh listing instead.
func (s *MCPService) retainedToolCatalogUsable(name string) bool {
	if !s.mcpToolListRefreshApplies(name) {
		return true
	}
	state := s.toolListRefreshStateFor(name)
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.decisionLocked(s.cachedToolListRevision(name), time.Now()) != mcpToolListRefresh
}

// retainedToolCatalogInventory rebuilds the discovery result of an already
// published catalog, mirroring Rust's retained catalog when a notification
// refresh fails. It returns nil when nothing was published yet.
func retainedToolCatalogInventory(status MCPServerStatus) *stdioInventory {
	if len(status.Tools) == 0 {
		return nil
	}
	return &stdioInventory{
		Tools:              append([]MCPToolInfo(nil), status.Tools...),
		Resources:          append([]MCPResource(nil), status.Resources...),
		ResourceTemplates:  append([]MCPResourceTemplate(nil), status.ResourceTemplates...),
		ToolListMeta:       cloneMCPRawMessage(status.ToolListMeta),
		ServerCapabilities: cloneMCPRawMessage(status.ServerCapabilities),
	}
}

// listCatalogIfStable lists one server and reports the catalog together with the
// revision it was listed at and whether that listing stayed stable across its
// own pages. A listing that raced a notification is reported as not stable, so
// it is never published as the current catalog (Rust #54656).
func (s *MCPService) listCatalogIfStable(name string, config *ServerConfig, threadID string, skipResourceTemplates bool) (*stdioInventory, uint64, bool, error) {
	revision := s.cachedToolListRevision(name)
	inventory, err := s.listInventoryForConfig(name, config, threadID, skipResourceTemplates)
	if err != nil {
		return nil, revision, false, err
	}
	return inventory, revision, s.cachedToolListRevision(name) == revision, nil
}

// discoverToolCatalogForStatus returns the catalog to publish for one status
// read, mirroring Rust's refresh_notified_catalog (Rust #54656): an ordinary
// server whose connection reported a tool-list change is listed again, and only
// a listing that stayed stable across the fetch is published - a listing
// invalidated while its pages were being fetched is retried once. A refresh
// that fails (or keeps churning) keeps the previously published catalog and
// starts a short retry cooldown, so a flapping server neither drops its catalog
// nor hammers the connection.
func (s *MCPService) discoverToolCatalogForStatus(name string, config *ServerConfig, threadID string, retained MCPServerStatus) (*stdioInventory, error) {
	skipResourceTemplates := retained.hostOwnedApps
	if !s.mcpToolListRefreshApplies(name) {
		return s.listInventoryForConfig(name, config, threadID, skipResourceTemplates)
	}
	state := s.toolListRefreshStateFor(name)
	state.mu.Lock()
	defer state.mu.Unlock()
	switch state.decisionLocked(s.cachedToolListRevision(name), time.Now()) {
	case mcpToolListInCooldown:
		// A failed or churning refresh keeps serving the catalog it published
		// before the notification arrived, and the server is not listed again
		// until the retry cooldown expires.
		if retainedInventory := retainedToolCatalogInventory(retained); retainedInventory != nil {
			return retainedInventory, nil
		}
	case mcpToolListNoChange:
		// Nothing was invalidated on this connection, so the read lists the
		// server exactly as a read without the feature does.
		inventory, revision, stable, err := s.listCatalogIfStable(name, config, threadID, skipResourceTemplates)
		if err != nil {
			return nil, err
		}
		if stable {
			state.publishedLocked(revision)
		}
		return inventory, nil
	}
	// A tools/list_changed notification invalidated the published catalog, so
	// the server is listed again; only a listing that stayed stable across its
	// own pages is published.
	var lastErr error
	for attempt := 0; attempt < mcpToolListNotificationRefreshAttempts; attempt++ {
		inventory, revision, stable, err := s.listCatalogIfStable(name, config, threadID, skipResourceTemplates)
		if err != nil {
			lastErr = err
			break
		}
		if !stable {
			// Another notification arrived while the pages were fetched, so
			// this listing is already stale; Rust retries it exactly once.
			continue
		}
		state.publishedLocked(revision)
		return inventory, nil
	}
	state.cooldownLocked(time.Now())
	if retainedInventory := retainedToolCatalogInventory(retained); retainedInventory != nil {
		// Keeping the previously published catalog is the Rust contract on this
		// path, so a failed refresh is not reported to the caller.
		return retainedInventory, nil
	}
	if lastErr == nil {
		// The listing kept changing while its pages were fetched, so no stable
		// catalog could be published for this read.
		lastErr = fmt.Errorf("MCP server %q changed its tool list while it was being listed", name)
	}
	return nil, lastErr
}

func (s *MCPService) populateStatusInventories(params *MCPListServerStatusParams, detail *MCPServerStatusDetail, observer MCPStartupObserver, servers []MCPServerStatus, configs map[string]ServerConfig, dynamicConfigs map[string]bool, threadID string) []MCPServerStatus {
	if !detail.includesTools() {
		return servers
	}
	resultCh := make(chan mcpInventoryStatusResult, len(servers))
	pending := make(map[int]string, len(servers))
	required := map[string]bool{}
	requiredPlugins := map[string]bool{}
	if params != nil {
		for _, name := range params.RequiredServers {
			if name = strings.TrimSpace(name); name != "" {
				required[name] = true
			}
		}
		for _, pluginID := range params.RequiredPlugins {
			if pluginID = strings.TrimSpace(pluginID); pluginID != "" {
				requiredPlugins[pluginID] = true
			}
		}
	}
	requiredIndexes := map[int]bool{}
	// Rust #48783: a selected serverName restricts discovery to that server, so
	// unrelated servers are neither started nor connected.
	selectedServer := mcpServerNameFilter(params)
	for i := range servers {
		name := servers[i].effectiveName()
		if selectedServer != "" && name != selectedServer {
			continue
		}
		config, ok := configs[name]
		if !ok {
			continue
		}
		if dynamicConfigs[name] && (params == nil || params.Detail == nil) {
			notifyMCPStartupObserver(observer, name, servers[i].State, servers[i].FailureReason, nil)
			continue
		}
		// Rust #48783 (ea64727556): a thread-scoped read of one selected server
		// answers from that thread's current connection and already materialized
		// tool catalog (`CodexThread::mcp_server_status_snapshot` ->
		// `McpRuntime::server_status_snapshot` ->
		// `collect_mcp_server_status_snapshot_from_manager`), so a status read
		// never repeats the initialize or tools/list round trips. That snapshot
		// only reads the manager's published server infos and tool catalog, so a
		// server the thread has not started stays dormant: the read must not open
		// a connection or materialize the catalog on this path (the reported
		// runtime status is the thread's current one, not a fresh startup), and
		// because nothing was materialized this call records no catalog source
		// and reports no size telemetry.
		if selectedServer != "" && threadID != "" {
			notifyMCPStartupObserver(observer, name, servers[i].State, servers[i].FailureReason, nil)
			continue
		}
		selectedPluginRequired := servers[i].source == CatalogSourceSelectedPlugin &&
			servers[i].PluginID != nil && requiredPlugins[*servers[i].PluginID]
		explicitlyRequired := required[name] || selectedPluginRequired
		// Rust #47935: startup_readiness = "catalog" may satisfy startup with an
		// eligible cached catalog, even on the blocking startup-validation path.
		// Rust #38217's lazy-startup behavior still applies to required servers
		// during non-blocking capture. Explicit turn/plugin requirements always
		// wait for a live connection.
		cachedStartupCandidate := !explicitlyRequired &&
			(config.StartupReadiness.Effective() == MCPStartupReadinessCatalog ||
				(params != nil && params.NonBlockingOptional &&
					servers[i].State == MCPServerReady && config.Required))
		if cachedStartupCandidate {
			if tools, ok := s.cachedCatalogStartupTools(name, &config, servers[i]); ok {
				servers[i].Tools = tools
				// Rust #51215: the retained catalog is the cached source, so the
				// raw size telemetry reports "cached" rather than a stale live
				// read.
				servers[i].catalogSource = BindingCatalogSourceCached
				notifyMCPStartupObserver(observer, name, MCPServerReady, nil, nil)
				continue
			}
		}
		notifyMCPStartupObserver(observer, name, MCPServerStarting, nil, nil)
		pending[i] = name
		if config.Required || required[name] || selectedPluginRequired || (IsCodexAppsMCPServerName(name) && len(servers[i].Tools) == 0) {
			requiredIndexes[i] = true
		}
		s.markServerStartupStarted(name)
		go func(index int, serverName string, serverConfig ServerConfig, status MCPServerStatus) {
			resultCh <- s.inventoryStatusForConfig(index, serverName, &serverConfig, status, detail.includesInventory(), threadID)
		}(i, name, config, cloneMCPServerStatus(servers[i]))
	}
	applyResult := func(result mcpInventoryStatusResult) {
		if _, ok := pending[result.Index]; !ok {
			return
		}
		delete(pending, result.Index)
		delete(requiredIndexes, result.Index)
		servers[result.Index] = result.Status
		name := result.Status.effectiveName()
		if result.Err != nil && result.Status.State == MCPServerFailed {
			observerErr := result.Err
			if result.Status.Error != nil {
				observerErr = errors.New(*result.Status.Error)
			}
			notifyMCPStartupObserver(observer, name, MCPServerFailed, result.Status.FailureReason, observerErr)
		} else {
			notifyMCPStartupObserver(observer, name, result.Status.State, result.Status.FailureReason, nil)
		}
	}
	// Rust #41199: a zero optional_mcp_startup_grace disables the shared grace,
	// so optional servers wait for their configured startup timeout instead of
	// being omitted after a fixed window. This is the blocking path.
	if params == nil || !params.NonBlockingOptional || params.OptionalStartupGrace <= 0 {
		for len(pending) > 0 {
			applyResult(<-resultCh)
		}
		s.recordBindingCatalogTelemetry(servers, threadID)
		return servers
	}

	grace := params.OptionalStartupGrace
	defaultDeadline := time.Now().Add(grace)
	optionalDeadlines := map[int]time.Time{}
	for index, name := range pending {
		if !requiredIndexes[index] {
			config := configs[name]
			agentPlugin := servers[index].PluginID != nil
			oauthResolved := s.authStatusForConfig(name, &config) == MCPAuthOAuth
			if key, eligible := mcpToolCatalogGraceKeyWithFeatures(name, &config, s.openAIForm, s.openAIForm && servers[index].hostOwnedApps, agentPlugin, oauthResolved); eligible {
				optionalDeadlines[index] = sharedOptionalStartupDeadlineForKey(key, defaultDeadline)
			} else {
				// OAuth / dynamically-resolved-credential HTTP configs stay out of
				// the shared catalog cache: each connection set warms its own.
				optionalDeadlines[index] = defaultDeadline
			}
		}
	}
	for len(pending) > 0 {
		now := time.Now()
		for index, deadline := range optionalDeadlines {
			if !deadline.After(now) {
				delete(optionalDeadlines, index)
				delete(pending, index)
			}
		}
		if len(pending) == 0 {
			break
		}
		var nextDeadline time.Time
		for _, deadline := range optionalDeadlines {
			if nextDeadline.IsZero() || deadline.Before(nextDeadline) {
				nextDeadline = deadline
			}
		}
		if nextDeadline.IsZero() {
			result := <-resultCh
			applyResult(result)
			continue
		}
		timer := time.NewTimer(time.Until(nextDeadline))
		select {
		case result := <-resultCh:
			if !timer.Stop() {
				<-timer.C
			}
			delete(optionalDeadlines, result.Index)
			applyResult(result)
		case <-timer.C:
		}
	}
	s.recordBindingCatalogTelemetry(servers, threadID)
	return servers
}

func (s *MCPService) inventoryStatusForConfig(index int, name string, config *ServerConfig, status MCPServerStatus, includeInventory bool, threadID string) mcpInventoryStatusResult {
	// The catalog comes from the server itself on this path, so the raw size
	// telemetry reports a live source even when discovery fails (Rust #51215).
	status.catalogSource = BindingCatalogSourceLive
	// Rust #54656: an ordinary server whose connection reported a tool-list
	// change is listed again before its catalog is published, and the previous
	// catalog is retained when that refresh fails.
	inventory, err := s.discoverToolCatalogForStatus(name, config, threadID, status)
	status.ServerCapabilities = nil
	// A failed discovery reports no catalog metadata either, so the wire field
	// stays null (Rust #54686).
	status.ToolListMeta = nil
	if inventory != nil {
		status.ServerCapabilities = cloneMCPRawMessage(inventory.ServerCapabilities)
	}
	if err == nil {
		status.Tools = inventory.Tools
		// The catalog metadata is published with the catalog it came from
		// (Rust #54686).
		status.ToolListMeta = cloneMCPRawMessage(inventory.ToolListMeta)
		if includeInventory {
			status.Resources = inventory.Resources
			status.ResourceTemplates = inventory.ResourceTemplates
		}
		status.State = MCPServerReady
		status.Error = nil
	} else if isRunnableMCPConfig(config) {
		message := err.Error()
		authStatus := s.authStatusForConfig(name, config)
		if display, ok := mcpInitAuthErrorDisplay(name, config, authStatus, err); ok {
			message = display
			if authStatus == MCPAuthOAuth {
				reason := mcpFailureReasonReauthenticationRequired
				status.FailureReason = &reason
			}
		}
		status.State = MCPServerFailed
		status.Error = &message
		status.ToolsError = &message
		// Rust #53672: startup recovery must not remove Codex Apps functions the
		// model already discovered. The failed state still reports the error, but
		// the retained catalog remains available to binding capture and a later
		// invocation can join the shared startup retry.
		if !IsCodexAppsMCPServerName(name) || len(status.Tools) == 0 {
			status.Tools = nil
		}
		status.Resources = nil
		status.ResourceTemplates = nil
	}
	status.AuthStatus = s.authStatusForConfig(status.effectiveName(), config)
	// Rust #44359: status-only discovery has no event channel to report a failed
	// connection attempt, so an OAuth credential the server rejected is reported
	// as logged out instead of retaining the credential-presence status. Other
	// statuses and non-authentication failures keep their status.
	if err != nil && status.AuthStatus == MCPAuthOAuth && mcpErrAuthenticationRequired(err) {
		status.AuthStatus = MCPAuthNotLoggedIn
	}
	s.recordInventoryStatus(status.effectiveName(), status, true, includeInventory)
	return mcpInventoryStatusResult{Index: index, Status: status, Err: err}
}

const mcpFailureReasonReauthenticationRequired = "reauthenticationRequired"

func notifyMCPStartupObserver(observer MCPStartupObserver, name string, status MCPServerStartupState, failureReason *string, err error) {
	if observer != nil {
		observer(name, status, cloneStringPtr(failureReason), err)
	}
}

// mcpInitAuthErrorDisplay returns the user-facing startup error for a server
// whose MCP initialization was rejected for authentication. It mirrors Rust's
// mcp_init_error_display: an OAuth credential that the server rejects is
// reported as reauthentication rather than a plain logged-out state.
func mcpInitAuthErrorDisplay(serverName string, config *ServerConfig, authStatus MCPAuthStatus, err error) (string, bool) {
	if !mcpErrAuthenticationRequired(err) {
		return "", false
	}
	recoveryHint := fmt.Sprintf("Run `codex mcp login %s`.", strings.TrimSpace(serverName))
	if config != nil && !config.IsLocalEnvironment() {
		recoveryHint = "Use your client's MCP OAuth sign-in flow."
	}
	state := "is not logged in"
	if authStatus == MCPAuthOAuth {
		state = "requires OAuth reauthentication"
	}
	return fmt.Sprintf("The %s MCP server %s. %s", strings.TrimSpace(serverName), state, recoveryHint), true
}

func mcpErrAuthenticationRequired(err error) bool {
	if err == nil {
		return false
	}
	// Rust #55691: an enterprise token-source failure classifies a required
	// sign-in by credential kind and exchange stage, so a resource authorization
	// rejection is not reported as a login failure. A raw sanitized failure keeps
	// its own classification.
	var tokenSourceErr *EMATokenSourceError
	if errors.As(err, &tokenSourceErr) {
		return tokenSourceErr.IsAuthenticationRequired()
	}
	var emaFailure *EMAAuthFailure
	if errors.As(err, &emaFailure) {
		return emaFailure.IsAuthenticationRequired()
	}
	var status *mcpHTTPStatusError
	return errors.As(err, &status) && (status.StatusCode == 401 || status.StatusCode == 407)
}

func (s *MCPService) recordInventoryStatus(name string, status MCPServerStatus, includeTools bool, includeInventory bool) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	s.mu.Lock()
	current, ok := s.servers[name]
	if !ok {
		s.mu.Unlock()
		return
	}
	current.State = status.State
	current.Error = cloneStringPtr(status.Error)
	current.AuthStatus = status.AuthStatus
	current.catalogSource = status.catalogSource
	// Capabilities come from the latest connection attempt; a failed
	// initialization clears them (Rust #44826).
	current.ServerCapabilities = cloneMCPRawMessage(status.ServerCapabilities)
	// The catalog metadata is retained next to the retained tool catalog, so a
	// later cached read reports it too (Rust #54686).
	current.ToolListMeta = cloneMCPRawMessage(status.ToolListMeta)
	if includeTools {
		current.Tools = append([]MCPToolInfo(nil), status.Tools...)
	}
	if includeInventory {
		current.Resources = append([]MCPResource(nil), status.Resources...)
		current.ResourceTemplates = append([]MCPResourceTemplate(nil), status.ResourceTemplates...)
	}
	s.servers[name] = cloneMCPServerStatus(current)
	if s.starting[name] <= 1 {
		delete(s.starting, name)
	} else {
		s.starting[name]--
	}
	config := s.configs[name]
	s.mu.Unlock()
	if status.State == MCPServerReady {
		oauthResolved := s.authStatusForConfig(name, &config) == MCPAuthOAuth
		if key, eligible := mcpToolCatalogGraceKeyWithFeatures(name, &config, s.openAIForm, s.openAIForm && status.hostOwnedApps, status.PluginID != nil, oauthResolved); eligible {
			clearSharedOptionalStartupDeadlineForKey(key)
		}
	}
}

func (s *MCPService) markServerStartupStarted(name string) {
	if s == nil || strings.TrimSpace(name) == "" {
		return
	}
	s.mu.Lock()
	if s.starting == nil {
		s.starting = map[string]int{}
	}
	s.starting[name]++
	s.mu.Unlock()
}

func sharedOptionalStartupDeadlineForKey(key string, fallback time.Time) time.Time {
	sharedOptionalMCPStartupGrace.Lock()
	defer sharedOptionalMCPStartupGrace.Unlock()
	if deadline, ok := sharedOptionalMCPStartupGrace.deadlines[key]; ok {
		return deadline
	}
	sharedOptionalMCPStartupGrace.deadlines[key] = fallback
	return fallback
}

func clearSharedOptionalStartupDeadlineForKey(key string) {
	sharedOptionalMCPStartupGrace.Lock()
	delete(sharedOptionalMCPStartupGrace.deadlines, key)
	sharedOptionalMCPStartupGrace.Unlock()
}

// hasExplicitHTTPAuthorization mirrors Rust's has_explicit_http_authorization
// (codex-rs/codex-mcp/src/server.rs): a streamable HTTP config has an explicit
// authorization only when it carries a non-empty, printable Authorization value
// in http_headers and does not defer the credential to a bearer token env var or
// env-supplied http headers.
func hasExplicitHTTPAuthorization(config *ServerConfig) bool {
	if config == nil || strings.TrimSpace(config.URL) == "" {
		return false
	}
	if strings.TrimSpace(config.BearerTokenEnvVar) != "" || len(config.EnvHTTPHeaders) > 0 {
		return false
	}
	for name, value := range config.HTTPHeaders {
		if !strings.EqualFold(strings.TrimSpace(name), "Authorization") || strings.TrimSpace(value) == "" {
			continue
		}
		valid := true
		for _, character := range []byte(value) {
			if character != '\t' && (character < ' ' || character == 0x7f) {
				valid = false
				break
			}
		}
		if valid {
			return true
		}
	}
	return false
}

// mcpToolCatalogCacheEligible mirrors Rust's ToolCatalogTransportIdentity::new
// HTTP branch (codex-rs/codex-mcp/src/tool_catalog_cache.rs): streamable HTTP
// configs participate in the process-scoped tool catalog cache only when their
// authentication identity can be derived safely. OAuth / scopes / oauth_resource
// configs, ChatGpt auth without explicit HTTP authorization, and dynamically
// resolved OAuth credentials are kept out of the shared cache. Stdio configs
// remain eligible as before.
func mcpToolCatalogCacheEligible(config *ServerConfig, oauthCredentialsResolved bool) bool {
	if config == nil || strings.TrimSpace(config.URL) == "" {
		return true
	}
	if strings.TrimSpace(config.OAuthClientID) != "" || strings.TrimSpace(config.OAuthResource) != "" || config.ScopesConfigured {
		return false
	}
	explicit := hasExplicitHTTPAuthorization(config)
	if strings.EqualFold(config.EffectiveAuth(), ServerAuthChatGPT) && !explicit {
		return false
	}
	if !explicit && oauthCredentialsResolved {
		return false
	}
	return true
}

// mcpToolCatalogGraceKey builds the shared optional-startup-grace identity for a
// server. The returned eligible flag mirrors Rust: only cache-eligible servers
// (stdio, or HTTP configs whose auth identity can be derived safely) share the
// process-wide catalog identity; OAuth / dynamically-resolved-credential HTTP
// configs stay out of the shared cache and each connection set warms its own
// catalog.
//
// For streamable HTTP configs the fingerprint additionally covers resolved
// bearer-token / env-header values and agent-plugin status, so catalogs are
// reused only across equivalent connections (Rust 0ca439900e).
func mcpToolCatalogGraceKey(name string, config *ServerConfig, openAIForm bool, agentPlugin bool, oauthCredentialsResolved bool) (string, bool) {
	return mcpToolCatalogGraceKeyWithFeatures(name, config, openAIForm, false, agentPlugin, oauthCredentialsResolved)
}

func mcpToolCatalogGraceKeyWithFeatures(name string, config *ServerConfig, openAIForm bool, userVerification bool, agentPlugin bool, oauthCredentialsResolved bool) (string, bool) {
	if config == nil {
		return strings.TrimSpace(name) + "\x00" + mcpConnectionCacheKeyWithFeatures(config, openAIForm, userVerification), true
	}
	// Rust #46042: a read-only server must not read or populate an unrestricted
	// shared catalog.
	if config.RequiresReadOnlyTools {
		return "", false
	}
	if strings.TrimSpace(config.Command) != "" {
		// Rust #57210 keys a discovering connection by the legacy catalog mode:
		// an unmarked helper is expected to fall back, and a modern negotiation
		// invalidates that legacy catalog instead of publishing into it.
		catalogConfig := config
		if catalogConfig.ProtocolMode == MCPProtocolDiscoverLocalStdio {
			cloned := cloneServerConfig(catalogConfig)
			cloned.ProtocolMode = MCPProtocolLegacy
			catalogConfig = &cloned
		}
		return strings.TrimSpace(name) + "\x00" + mcpConnectionCacheKeyWithFeatures(catalogConfig, openAIForm, userVerification), true
	}
	if strings.TrimSpace(config.URL) == "" {
		return "", false
	}
	if !mcpToolCatalogCacheEligible(config, oauthCredentialsResolved) {
		return "", false
	}
	key := mcpConnectionCacheKeyWithFeatures(config, openAIForm, userVerification)
	if agentPlugin {
		key += "|agentPlugin=1"
	}
	return strings.TrimSpace(name) + "\x00" + key, true
}

func (s *MCPService) OauthLogin(params *MCPServerOauthLoginParams) (*MCPServerOauthLoginResponse, error) {
	if params == nil {
		params = &MCPServerOauthLoginParams{}
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		name = strings.TrimSpace(params.ServerName)
	}
	if name == "" {
		return nil, invalidMCPRequest("name is required")
	}
	registration, err := mcpServerOauthClientRegistration(params)
	if err != nil {
		return nil, err
	}
	// Rust #56266: a requirements denial also blocks the standalone login HTTP
	// client (including its headers helper), so report it instead of minting an
	// unusable authorization URL.
	if config, ok := s.serverConfigIncludingDisabled(name); ok && managedconfig.MCPDisabledByManagedRequirements(config.DisabledReason) {
		return nil, invalidMCPRequest("the MCP server is disabled by managed requirements")
	}
	// Rust #49276: the login attempt id is minted by the app-server when the
	// login starts, returned in this response and echoed in the completion
	// notification, so a client can match the two.
	loginID := NewMCPOAuthLoginID()
	url := "http://localhost/oauth/" + name
	if config, ok := s.serverConfig(name); ok && strings.TrimSpace(config.URL) != "" {
		if config.EffectiveAuth() == ServerAuthEMAAuth {
			// Enterprise MCP authorization is managed by Codex account
			// enterprise sign-in (Rust #55334); ordinary MCP OAuth login is
			// blocked for EMA.
			return nil, invalidMCPRequest("enterprise MCP authorization uses account enterprise sign-in, not MCP OAuth login")
		}
		if config.EffectiveAuth() == ServerAuthChatGPT && !config.IsLocalEnvironment() {
			return nil, invalidMCPRequest("OAuth login is not supported for executor-owned ChatGPT MCP servers")
		}
		if loginURL, ok, loginErr := s.startOAuthLoginServer(name, &config, params, loginID); loginErr != nil {
			// Rust surfaces a refused authorization server out of
			// `perform_oauth_login`; never hide it behind a guessed URL.
			return nil, loginErr
		} else if ok {
			url = loginURL
		} else if registration == MCPServerOauthClientRegistrationDcr && strings.TrimSpace(config.OAuthClientID) == "" {
			// Rust 6dc3ac8721: forced DCR must not silently fall back to a
			// non-registered authorize URL when the server cannot register.
			return nil, invalidMCPRequest("MCP OAuth login requires dynamic client registration (clientRegistration=dcr), but the server does not advertise a registration endpoint")
		} else {
			client := s.httpClientForServer(name, &config).oauthHTTPClient(mcpOAuthLoginDiscoveryTimeout(params.TimeoutSecs))
			fallbackURL, fallbackErr := buildMCPOAuthURLForLogin(&config, params.Scopes, params.TimeoutSecs, client)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			url = fallbackURL
		}
	}
	return &MCPServerOauthLoginResponse{AuthorizationURL: url, URL: url, LoginID: &loginID}, nil
}

func mcpServerOauthClientRegistration(params *MCPServerOauthLoginParams) (MCPServerOauthClientRegistration, error) {
	if params == nil || params.ClientRegistration == nil {
		return MCPServerOauthClientRegistrationAuto, nil
	}
	value := MCPServerOauthClientRegistration(strings.ToLower(strings.TrimSpace(string(*params.ClientRegistration))))
	if !value.Valid() {
		return MCPServerOauthClientRegistrationAuto, invalidMCPRequest("clientRegistration must be one of: auto, cimd, dcr")
	}
	return value, nil
}

func (s *MCPService) OauthCancel(params *MCPServerOauthCancelParams) (*MCPServerOauthCancelResponse, error) {
	if params == nil {
		params = &MCPServerOauthCancelParams{}
	}
	name := strings.TrimSpace(firstNonEmptyMCP(params.Name, params.ServerName))
	if name == "" {
		return nil, invalidMCPRequest("name is required")
	}
	var login *OAuthLoginServer
	if s != nil {
		s.mu.Lock()
		if s.oauthLogins != nil {
			login = s.oauthLogins[name]
			delete(s.oauthLogins, name)
		}
		s.mu.Unlock()
	}
	if login != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := login.Cancel(ctx); err != nil {
			return nil, err
		}
	}
	return &MCPServerOauthCancelResponse{}, nil
}

func (s *MCPService) startOAuthLoginServer(name string, config *ServerConfig, params *MCPServerOauthLoginParams, loginID string) (string, bool, error) {
	if s == nil || config == nil || params == nil || strings.TrimSpace(config.URL) == "" {
		return "", false, nil
	}
	registration, err := mcpServerOauthClientRegistration(params)
	if err != nil {
		return "", false, nil
	}
	store := s.oauthStoreForConfig(config)
	if store == nil {
		return "", false, nil
	}
	callbackTimeout := mcpOAuthLoginTimeout(params.TimeoutSecs)
	client := s.httpClientForServer(name, config).oauthHTTPClient(0)
	ctx := context.Background()
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, config.URL, client)
	if mcpOAuthIssuerBindingRejected(err) {
		return "", false, err
	}
	if err != nil || discovery == nil || strings.TrimSpace(discovery.AuthorizationEndpoint) == "" || strings.TrimSpace(discovery.TokenEndpoint) == "" {
		return "", false, nil
	}
	clientID := strings.TrimSpace(config.OAuthClientID)
	if strings.TrimSpace(config.OAuthClientSecret) != "" && clientID == "" {
		return "", false, errors.New("oauth.client_secret requires oauth.client_id")
	}
	if clientID == "" && strings.TrimSpace(discovery.RegistrationEndpoint) == "" {
		return "", false, nil
	}
	login, err := StartOAuthLoginServer(ctx, &OAuthLoginServerOptions{
		ServerName:                 config.OAuthCredentialName(name),
		ServerURL:                  config.URL,
		ClientID:                   clientID,
		ClientSecret:               config.OAuthClientSecret,
		OmitClientSecretFromRecord: strings.TrimSpace(config.OAuthClientSecret) != "",
		Issuer:                     strings.TrimSpace(discovery.Issuer),
		RegistrationEndpoint:       discovery.RegistrationEndpoint,
		AuthorizationEndpoint:      discovery.AuthorizationEndpoint,
		TokenEndpoint:              discovery.TokenEndpoint,
		Resource:                   firstNonEmptyMCP(config.OAuthResource, discovery.Resource),
		Scopes:                     params.Scopes,
		Store:                      store,
		HTTPClient:                 client,
		Timeout:                    callbackTimeout,
		Port:                       config.OAuthCallbackPort,
		RedirectURL:                config.OAuthCallbackURL,
		ClientRegistration:         registration,
		CIMDAdvertised:             &discovery.ClientIDMetadataDocumentSupported,
		PublicClientAuth:           &discovery.PublicClientTokenAuthSupported,
		CallbackMode:               discovery.CallbackMode,
	})
	if err != nil {
		return "", false, nil
	}
	threadID := ""
	if params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
	}
	s.trackOAuthLogin(name, threadID, loginID, login)
	return login.AuthorizationURL, true, nil
}

func mcpOAuthLoginTimeout(timeoutSecs *uint64) time.Duration {
	if timeoutSecs == nil || *timeoutSecs == 0 {
		return mcpOAuthDependencyLoginTimeout
	}
	const maxDurationSeconds = uint64((1<<63 - 1) / int64(time.Second))
	if *timeoutSecs > maxDurationSeconds {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(*timeoutSecs) * time.Second
}

func (s *MCPService) oauthStoreForConfig(config *ServerConfig) *OAuthStore {
	if s == nil || config == nil {
		return nil
	}
	if s.oauth != nil {
		return s.oauth
	}
	if home := strings.TrimSpace(config.CodexHome); home != "" {
		return NewOAuthStoreWithMode(home, config.OAuthCredentialsStoreMode)
	}
	return nil
}

func (s *MCPService) trackOAuthLogin(name string, threadID string, loginID string, login *OAuthLoginServer) {
	if s == nil || login == nil {
		return
	}
	name = strings.TrimSpace(name)
	threadID = strings.TrimSpace(threadID)
	if name == "" {
		return
	}
	var previous *OAuthLoginServer
	s.mu.Lock()
	if s.oauthLogins == nil {
		s.oauthLogins = map[string]*OAuthLoginServer{}
	}
	previous = s.oauthLogins[name]
	s.oauthLogins[name] = login
	s.mu.Unlock()
	if previous != nil && previous != login {
		_ = previous.Cancel(context.Background())
	}
	go func() {
		result := <-login.Done()
		s.mu.Lock()
		if s.oauthLogins[name] == login {
			delete(s.oauthLogins, name)
		}
		s.mu.Unlock()
		s.clearHTTPClients()
		s.notifyOAuthLoginCompleted(name, threadID, loginID, result)
	}()
}

func (s *MCPService) notifyOAuthLoginCompleted(name string, threadID string, loginID string, result *OAuthLoginServerResult) {
	completion := &MCPOAuthLoginCompletion{
		Name:     name,
		ThreadID: threadID,
		// Rust #49276: the completion carries the id minted when the login
		// started, so it matches OauthLogin's response for the same attempt.
		LoginID: loginID,
		Success: result != nil && result.Error == nil,
	}
	if result != nil && result.Error != nil {
		completion.Error = result.Error.Error()
	}
	completion = normalizeMCPOAuthLoginCompletion(completion)
	handler := s.oauthLoginCompletionHandler()
	if handler != nil {
		handler.HandleMCPOAuthLoginCompleted(context.Background(), completion)
	}
	s.mu.Lock()
	listeners := make([]MCPOAuthLoginCompletionHandlerFunc, 0, len(s.oauthCompleteListeners))
	for _, listener := range s.oauthCompleteListeners {
		listeners = append(listeners, listener)
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(context.Background(), completion)
	}
}

func (s *MCPService) Refresh() *MCPServerRefreshResponse {
	if s != nil {
		s.mu.Lock()
		s.generation++
		s.resourceGeneration++
		s.mu.Unlock()
		s.clearResourceCache()
		s.clearHTTPClients()
		s.clearStdioClients()
	}
	return &MCPServerRefreshResponse{}
}

func (s *MCPService) Generation() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// ResourceCacheGeneration identifies the current orchestrator resource cache
// generation without treating unrelated catalog/runtime updates as a refresh.
func (s *MCPService) ResourceCacheGeneration() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resourceGeneration
}

// InvalidateResourceCaches forces resource catalogs and contents to be fetched
// again while allowing the underlying MCP connections to be reused.
func (s *MCPService) InvalidateResourceCaches() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.resourceGeneration++
	s.mu.Unlock()
	s.clearResourceCache()
}

func (s *MCPService) ReadResource(params *MCPResourceReadParams) (*MCPResourceReadResponse, error) {
	if params == nil {
		params = &MCPResourceReadParams{}
	}
	server := strings.TrimSpace(params.Server)
	if server == "" {
		server = strings.TrimSpace(params.ServerName)
	}
	uri := strings.TrimSpace(params.URI)
	if server == "" || uri == "" {
		return nil, invalidMCPRequest("server and uri are required")
	}
	if err := validateMCPResourceOrigin(params); err != nil {
		return nil, err
	}
	if err := validateMCPResourceTarget(params, server); err != nil {
		return nil, err
	}
	if err := s.requiredServerAvailable(server); err != nil {
		return nil, err
	}
	threadID := threadIDFromResourceReadParams(params)
	roots := s.rootsForThread(threadID)
	cacheKey := &MCPResourceCacheKey{
		Server:   server,
		URI:      uri,
		ThreadID: threadID,
		RootsKey: mcpRootsCacheKey(roots),
		Scope:    resourceReadScopeKey(params),
		Target:   resourceReadTargetKey(params),
	}
	if cached, ok := s.readCachedResource(cacheKey); ok {
		return cached, nil
	}
	request := *params
	request.Server = server
	request.ServerName = ""
	request.URI = uri
	var response *MCPResourceReadResponse
	var err error
	setOrigin := func() {
		if response != nil && strings.TrimSpace(params.OriginCallID) != "" {
			response.OriginCallID = strings.TrimSpace(params.OriginCallID)
		}
	}
	if config, ok := s.serverConfig(server); ok {
		if strings.TrimSpace(config.URL) != "" {
			if err := ValidateServerAuth(server, &config); err != nil {
				return nil, err
			}
			response, err = readMCPHTTPResourceWithClient(s.httpClientForServer(server, &config), server, roots, s.elicitationHandlerForServer(server), s.progressHandler(), &request)
			if err != nil {
				return nil, err
			}
			setOrigin()
			s.writeCachedResource(cacheKey, response)
			return response, nil
		}
		if strings.TrimSpace(config.Command) != "" {
			response, err = readMCPStdioResourceWithLease(s.stdioClientForServer(server, &config), s.stdioConnectionLease(server), server, roots, s.elicitationHandlerForServer(server), s.progressHandler(), &request)
			if err != nil {
				return nil, err
			}
			setOrigin()
			s.writeCachedResource(cacheKey, response)
			return response, nil
		}
	}
	response = &MCPResourceReadResponse{Contents: []MCPResourceContent{{URI: uri, MimeType: "text/plain", Text: ""}}}
	setOrigin()
	s.writeCachedResource(cacheKey, response)
	return response, nil
}

// validateMCPResourceOrigin mirrors Rust #39187/#39244: scoped reads carry
// either a connector id (hosted app resources) or an originating call id;
// providing both, or a connector without the app-only surface, is rejected.
func validateMCPResourceOrigin(params *MCPResourceReadParams) error {
	if params == nil {
		return nil
	}
	origin := strings.TrimSpace(params.OriginCallID)
	connector := strings.TrimSpace(params.ConnectorID)
	if origin != "" && connector != "" {
		return invalidMCPRequest("resource read cannot combine originCallId and connectorId")
	}
	if origin != "" && strings.ContainsAny(origin, " \t\r\n") {
		return invalidMCPRequest("originCallId must be a single call id")
	}
	return nil
}

// validateMCPResourceTarget mirrors Rust #47248: a direct target is valid only
// for codex_apps, needs a non-empty connector id, and needs an explicit linkId
// that is either a real account string or null. An originCallId scope wins, so
// a target supplied alongside one is ignored rather than rejected.
func validateMCPResourceTarget(params *MCPResourceReadParams, server string) error {
	if params == nil || params.Target == nil || strings.TrimSpace(params.OriginCallID) != "" {
		return nil
	}
	target := params.Target
	invalidLink := !target.LinkIDSet
	if !invalidLink && target.LinkID != nil {
		link := strings.TrimSpace(*target.LinkID)
		invalidLink = link == "" || strings.HasPrefix(link, "synthetic_link::")
	}
	if !IsCodexAppsMCPServerName(server) || strings.TrimSpace(target.ConnectorID) == "" || invalidLink {
		return invalidMCPRequest("target requires codex_apps, a connectorId, and a real linkId or null")
	}
	return nil
}

// resourceReadScopeKey distinguishes cached reads by originating call scope so
// a resource fetched for one app call is never served for another.
func resourceReadScopeKey(params *MCPResourceReadParams) string {
	if params == nil {
		return ""
	}
	return strings.TrimSpace(params.OriginCallID)
}

// resourceReadTargetKey distinguishes cached reads by direct app/account target
// (Rust #47248): the same URI under two accounts must not share contents.
func resourceReadTargetKey(params *MCPResourceReadParams) string {
	if params == nil || params.Target == nil || strings.TrimSpace(params.OriginCallID) != "" {
		return ""
	}
	target := params.Target
	link := "null"
	if target.LinkID != nil {
		link = "link:" + strings.TrimSpace(*target.LinkID)
	}
	return "connector:" + strings.TrimSpace(target.ConnectorID) + "|" + link
}

func (s *MCPService) CallTool(params *MCPToolCallParams) (*MCPToolCallResponse, error) {
	if params == nil {
		return nil, invalidMCPRequest("server and tool are required")
	}
	server := strings.TrimSpace(params.Server)
	if server == "" {
		server = strings.TrimSpace(params.ServerName)
	}
	tool := strings.TrimSpace(params.Tool)
	if tool == "" {
		tool = strings.TrimSpace(params.ToolName)
	}
	if server == "" || tool == "" {
		return nil, invalidMCPRequest("server and tool are required")
	}
	if err := s.requiredServerAvailable(server); err != nil {
		return nil, err
	}
	// The sandbox-state contract is negotiated per connection: the server
	// advertises `codex/sandbox-state` (or its deprecated `-meta` alias) on
	// initialize/server-discover, and the connection records it (Rust #53709's
	// rmcp_client capability gate). Resolve the transport first so the same
	// negotiated connection also answers the capability question, then hand the
	// call its sandbox policy and launcher metadata.
	var httpTarget *httpClient
	var stdioTarget *stdioClient
	var serverConfigValue *ServerConfig
	serverEnvironmentID := ""
	if config, ok := s.serverConfig(server); ok {
		configCopy := config
		serverConfigValue = &configCopy
		serverEnvironmentID = config.EffectiveEnvironmentID()
		if strings.TrimSpace(config.URL) != "" {
			if err := ValidateServerAuth(server, &config); err != nil {
				return nil, err
			}
			httpTarget = s.httpClientForServer(server, &config)
		} else if strings.TrimSpace(config.Command) != "" {
			stdioTarget = s.stdioClientForServer(server, &config)
		}
	}
	effective, meta := s.prepareToolCallSandboxState(server, serverEnvironmentID, stdioTarget != nil, params, httpTarget.sandboxStateMetaNegotiated(), stdioTarget.sandboxStateMetaNegotiated())
	// The read-only marker belongs to the resolved metadata, so it survives a
	// resolver rebuild. The policy metadata itself is resolved against the
	// session that sends each attempt, while caller-supplied policy metadata is
	// always discarded (Rust #56278).
	meta = mcpMetaWithReadOnlyTools(meta, serverConfigValue)
	metaForSession := s.mcpNetworkPolicyMetaForToolCall(server, serverConfigValue, effective.ServerEnvironmentID, meta)
	roots := s.rootsForThread(effective.ThreadID)
	if httpTarget != nil {
		ctx := params.Context
		if ctx == nil {
			ctx = context.Background()
		}
		return callMCPHTTPToolWithClient(ctx, httpTarget, server, effective.ThreadID, effective.TurnID, effective.ItemID, roots, s.elicitationHandlerForServer(server), s.progressHandler(), tool, effective.Arguments, meta, metaForSession)
	}
	if stdioTarget != nil {
		resolvedMeta, err := metaForSession(stdioTarget.serverCapabilitiesRaw())
		if err != nil {
			return nil, err
		}
		return callMCPStdioToolWithLease(stdioTarget, s.stdioConnectionLease(server), server, effective.ThreadID, effective.TurnID, effective.ItemID, roots, s.elicitationHandlerForServer(server), s.progressHandler(), tool, effective.Arguments, resolvedMeta)
	}
	encoded, err := json.Marshal(params.Arguments)
	if err != nil {
		return nil, err
	}
	return &MCPToolCallResponse{Content: []MCPToolCallContent{{Type: "text", Text: string(encoded)}}}, nil
}

// prepareToolCallSandboxState is the production sequence every tools/call runs:
// complete the sandbox policy/launcher fields from the runtime for the
// connection's negotiated capability, then build the request `_meta`.
func (s *MCPService) prepareToolCallSandboxState(server string, environmentID string, localStdio bool, params *MCPToolCallParams, httpNegotiated bool, stdioNegotiated bool) (*MCPToolCallParams, any) {
	effective := s.sandboxStateToolCallParams(server, environmentID, localStdio, params, httpNegotiated, stdioNegotiated)
	return effective, s.augmentToolCallMeta(effective)
}

// sandboxStateToolCallParams returns the call parameters with the sandbox
// policy and launcher metadata the server's negotiated capability entitles it
// to. Rust mcp_tool_call.rs resolves all of this from the prepared call's
// runtime config; callers that already set a field keep their value.
func (s *MCPService) sandboxStateToolCallParams(server string, environmentID string, localStdio bool, params *MCPToolCallParams, httpNegotiated bool, stdioNegotiated bool) *MCPToolCallParams {
	clone := *params
	clone.SupportsSandboxStateMeta = params.SupportsSandboxStateMeta || httpNegotiated || stdioNegotiated
	if !clone.SupportsSandboxStateMeta {
		return &clone
	}
	if clone.ServerEnvironmentID == "" {
		clone.ServerEnvironmentID = environmentID
	}
	if clone.PermissionProfile == "" {
		if profile := s.permissionProfileJSONForServer(server); profile != "" {
			clone.PermissionProfile = profile
		}
	}
	if clone.SandboxCWD == "" {
		clone.SandboxCWD = s.sandboxCWDForServer(server, clone.ServerEnvironmentID)
	}
	if clone.CodexLinuxSandboxExe == "" {
		clone.CodexLinuxSandboxExe = s.codexLinuxSandboxExe
	}
	if !clone.UseLegacyLandlock {
		clone.UseLegacyLandlock = s.useLegacyLandlock
	}
	if !clone.UseMXC {
		clone.UseMXC = s.environmentUseMXC[clone.ServerEnvironmentID]
	}
	if clone.SandboxLauncherExecutable == "" {
		clone.SandboxLauncherExecutable = sandboxLauncherExecutable(s.codexSelfExe, clone.ServerEnvironmentID, localStdio)
	}
	return &clone
}

// permissionProfileJSONForServer serializes the authority published for the
// server (falling back to the thread authority) as the opaque policy document
// the sandbox CLI parses.
// permissionProfileForServer returns the authority published for a server,
// falling back to the thread authority (Rust permission_profile_for_server).
func (s *MCPService) permissionProfileForServer(server string) *sandbox.PermissionProfile {
	if s == nil {
		return nil
	}
	if profile, ok := s.PermissionProfileForServer(server); ok && profile != nil {
		return profile
	}
	return s.PermissionProfile()
}

func (s *MCPService) permissionProfileJSONForServer(server string) string {
	profile := s.permissionProfileForServer(server)
	if profile == nil {
		return ""
	}
	encoded, err := sandbox.RuntimePermissionProfileJSON(*profile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(encoded)
}

// sandboxCWDForServer resolves the sandbox cwd for the server's environment.
// Rust prefers the captured environment handle's cwd and falls back to the turn
// cwd for the default (local) environment; Go's runtime config exposes the
// per-server cwd as the local carrier.
func (s *MCPService) sandboxCWDForServer(server string, environmentID string) string {
	if s == nil {
		return ""
	}
	if cwd := strings.TrimSpace(s.environmentCWDs[environmentID]); cwd != "" {
		return cwd
	}
	config, ok := s.serverConfig(server)
	if !ok {
		return ""
	}
	return strings.TrimSpace(config.CWD)
}

// sandboxStateMetaNegotiated reports whether this connection's initialize or
// server-discover response advertised a sandbox-state capability.
func (c *httpClient) sandboxStateMetaNegotiated() bool {
	return c != nil && c.supportsSandboxStateMetaCapability
}

func (c *stdioClient) sandboxStateMetaNegotiated() bool {
	return c != nil && c.supportsSandboxStateMetaCapability
}

func (s *MCPService) augmentToolCallMeta(params *MCPToolCallParams) any {
	meta := withMCPToolCallIDsMeta(params.Meta, params)
	if callID := strings.TrimSpace(params.CallID); callID != "" {
		// Rust 248d8c0e22: include the tool call ID in _meta.callId for every
		// MCP tool request.
		if metaMap, ok := meta.(map[string]any); ok {
			out := cloneAnyMap(metaMap)
			if out == nil {
				out = map[string]any{}
			}
			out["callId"] = callID
			meta = out
		} else if meta == nil {
			meta = map[string]any{"callId": callID}
		}
	}
	if !params.SupportsSandboxStateMeta {
		return s.attachTrustedAccess(meta, params)
	}
	// Rust mcp_tool_call.rs resolves the sandbox cwd for the server's
	// environment and returns the metadata untouched when it is unknown.
	if strings.TrimSpace(params.SandboxCWD) == "" {
		return meta
	}
	var useLegacyLandlock *bool
	if params.UseLegacyLandlock {
		val := true
		useLegacyLandlock = &val
	}
	sandboxState := &SandboxState{
		PermissionProfile:    json.RawMessage(strings.TrimSpace(params.PermissionProfile)),
		SandboxCWD:           params.SandboxCWD,
		CodexLinuxSandboxExe: params.CodexLinuxSandboxExe,
		UseLegacyLandlock:    useLegacyLandlock,
		UseMXC:               params.UseMXC,
	}
	return s.attachTrustedAccess(augmentSandboxMetadata(meta, sandboxState, params.SandboxLauncherExecutable), params)
}

// attachTrustedAccess attaches the host-owned openai/entitlementContext
// metadata when the call is from an eligible plugin MCP server and the service
// carries a TrustedAccessContext (Rust #41005). Caller-supplied entitlement
// context is replaced by a fresh verified (or unknown) result.
func (s *MCPService) attachTrustedAccess(meta any, params *MCPToolCallParams) any {
	if s == nil || s.TrustedAccess == nil || params == nil || !trustedAccessEligible(params) {
		return meta
	}
	var metaMap map[string]any
	switch current := meta.(type) {
	case map[string]any:
		metaMap = current
	case nil:
		metaMap = map[string]any{}
	default:
		return meta
	}
	return s.TrustedAccess.addContext(cloneAnyMap(metaMap))
}

// trustedAccessEligible reports whether a plugin MCP call is eligible for
// host-owned entitlement metadata: a local (default environment) stdio call
// with no arguments. The full plugin-ownership + read-only annotation +
// requested-entitlement checks are performed by the app-server layer; this
// helper keeps the attachment scoped to local stdio zero-argument calls.
func trustedAccessEligible(params *MCPToolCallParams) bool {
	if params == nil {
		return false
	}
	if params.ServerEnvironmentID != "" && params.ServerEnvironmentID != DefaultMCPServerEnvironmentID {
		return false
	}
	switch arguments := params.Arguments.(type) {
	case nil:
	case map[string]any:
		if len(arguments) > 0 {
			return false
		}
	case string:
		if strings.TrimSpace(arguments) != "" {
			return false
		}
	default:
		return false
	}
	return true
}

const (
	mcpToolSessionIDMetaKey = "sessionId"
	mcpToolWindowIDMetaKey  = "windowId"
	mcpToolItemIDMetaKey    = "itemId"
)

// withMCPToolCallIDsMeta mirrors Rust's with_mcp_tool_call_ids_meta
// (#40866/#45409): every MCP request reports `threadId` and `sessionId`, the
// call's originating `windowId`, and `itemId` when the origin resolved to a
// Responses item. Stale values in the incoming meta are replaced, and a meta
// document that is not an object is passed through untouched, as Rust does.
func withMCPToolCallIDsMeta(meta any, params *MCPToolCallParams) any {
	threadID := strings.TrimSpace(params.ThreadID)
	sessionID := strings.TrimSpace(params.SessionID)
	windowID := strings.TrimSpace(params.WindowID)
	itemID := strings.TrimSpace(params.OriginItemID)
	if threadID == "" && sessionID == "" && windowID == "" && itemID == "" {
		return cloneJSONValue(meta)
	}
	out := map[string]any{}
	if meta != nil {
		values, ok := meta.(map[string]any)
		if !ok {
			return cloneJSONValue(meta)
		}
		out = cloneAnyMap(values)
		if out == nil {
			out = map[string]any{}
		}
	}
	if threadID != "" {
		out[mcpToolThreadIDMetaKey] = threadID
	}
	if sessionID != "" {
		out[mcpToolSessionIDMetaKey] = sessionID
	}
	if windowID != "" {
		out[mcpToolWindowIDMetaKey] = windowID
	}
	if itemID != "" {
		out[mcpToolItemIDMetaKey] = itemID
	}
	return out
}

// serverConfigIncludingDisabled returns a configured server even when it is
// disabled, so callers can report why (Rust #56266).
func (s *MCPService) serverConfigIncludingDisabled(name string) (ServerConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if config, ok := s.configs[name]; ok {
		return cloneServerConfig(&config), true
	}
	if config, ok := s.disabledConfigs[name]; ok {
		return cloneServerConfig(&config), true
	}
	return ServerConfig{}, false
}

func (s *MCPService) serverConfig(name string) (ServerConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configs == nil {
		return ServerConfig{}, false
	}
	config, ok := s.configs[name]
	if !ok || !config.Enabled {
		return ServerConfig{}, false
	}
	return cloneServerConfig(&config), true
}

// ServerConfigForServer returns the effective runtime configuration for an
// enabled server. It mirrors Rust's mcp_server_catalog.server() lookup used to
// apply per-server tool exposure policies (Rust 51c9ed6d4f).
func (s *MCPService) ServerConfigForServer(name string) (ServerConfig, bool) {
	return s.serverConfig(name)
}

// ResolvedServerForServer returns the effective registration for an enabled
// server: its winning source, plugin attribution, and config. It mirrors the
// resolved-catalog lookup Rust's AgentMcpMetadataRecipient performs (#58378).
func (s *MCPService) ResolvedServerForServer(name string) (ResolvedServer, bool) {
	if s == nil {
		return ResolvedServer{}, false
	}
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	config, ok := s.configs[name]
	if !ok || !config.Enabled {
		return ResolvedServer{}, false
	}
	status, ok := s.servers[name]
	if !ok {
		return ResolvedServer{}, false
	}
	resolved := ResolvedServer{
		Source:           status.source,
		Config:           cloneServerConfig(&config),
		CredentialPolicy: EffectiveCredentialPolicy(config.CredentialPolicy),
	}
	if status.PluginID != nil {
		if pluginID := strings.TrimSpace(*status.PluginID); pluginID != "" {
			resolved.PluginAttribution = &PluginAttribution{PluginID: pluginID, HostRoot: status.pluginHostRoot}
		}
	}
	return resolved, true
}

// serverEnvironmentID reports the environment that owns one runtime server
// (Rust McpRuntimeContext::server_environment_id). Servers without a runtime
// configuration report no owner.
func (s *MCPService) serverEnvironmentID(server string) string {
	config, ok := s.ServerConfigForServer(server)
	if !ok {
		return ""
	}
	return strings.TrimSpace(config.EffectiveEnvironmentID())
}

func (s *MCPService) listInventoryForConfig(name string, config *ServerConfig, threadID string, skipResourceTemplates bool) (*stdioInventory, error) {
	// Rust #48143: an executor-discovered declaration may only reach a remote
	// HTTP transport that needs no host environment headers, helpers or bearer
	// tokens, and the rejection happens before any host value is read.
	if err := ValidateExecutorDiscoveryCredentialPolicy(name, config); err != nil {
		return nil, err
	}
	roots := s.rootsForThread(threadID)
	if config != nil && strings.TrimSpace(config.URL) != "" {
		if err := ValidateServerAuth(name, config); err != nil {
			return nil, err
		}
		return listMCPHTTPInventoryWithOptions(s.httpClientForServer(name, config), name, threadID, roots, skipResourceTemplates)
	}
	if config != nil && strings.TrimSpace(config.Command) != "" {
		return listMCPStdioInventoryWithOptions(s.stdioClientForServer(name, config), name, threadID, roots, skipResourceTemplates)
	}
	return listMCPInventory(config)
}

func (s *MCPService) httpClientForServer(name string, config *ServerConfig) *httpClient {
	if config == nil {
		return newMCPHTTPClient(nil)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(config.URL)
	}
	if s == nil || name == "" {
		cloned := cloneServerConfig(config)
		return newMCPHTTPClient(&cloned)
	}
	s.mu.Lock()
	if s.httpClients == nil {
		s.httpClients = map[string]*cachedMCPHTTPClient{}
	}
	openAIForm := s.openAIForm
	userVerification := s.serverAllowsUserVerificationLocked(name)
	sharedHTTPClient := s.sharedHTTPClient
	sharedHTTPClientKey := s.sharedHTTPClientKey
	if config.HTTPClient != nil {
		sharedHTTPClient = config.HTTPClient
		sharedHTTPClientKey = mcpHTTPDoerIdentity(config.HTTPClient)
	}
	key := s.mcpHTTPClientCacheKey(config, openAIForm, userVerification, sharedHTTPClientKey)
	if cached := s.httpClients[name]; cached != nil && cached.key == key && cached.client != nil && !cached.client.isClosed() &&
		(cached.client.isInitialized() || cached.startupTimeout == config.StartupTimeout) {
		s.mu.Unlock()
		return cached.client
	}
	old := s.deleteHTTPClientLocked(name)
	cloned := cloneServerConfig(config)
	client := newMCPHTTPClientWithCapabilities(&cloned, mcpClientFeatures{
		OpenAIForm:       openAIForm,
		UserVerification: userVerification,
		EMAAuth:          cloned.EMAAuthExtension,
		NetworkPolicy:    s.networkPolicyAdvertisedForServer(config),
	}, sharedHTTPClient)
	s.httpClients[name] = &cachedMCPHTTPClient{key: key, client: client, startupTimeout: config.StartupTimeout}
	s.mu.Unlock()
	closeHTTPClients([]*httpClient{old})
	return client
}

func mcpHTTPConnectionCacheKey(config *ServerConfig, openAIForm bool, userVerification bool, sharedHTTPClientKey string) string {
	return mcpConnectionCacheKeyWithFeatures(config, openAIForm, userVerification) + "|httpClient=" + sharedHTTPClientKey
}

// mcpStdioClientCacheKey scopes a cached connection to the capabilities it
// advertised, so a call can never reuse a connection that did not offer the
// network policy capability (Rust #56278).
func (s *MCPService) mcpStdioClientCacheKey(config *ServerConfig, openAIForm bool, userVerification bool) string {
	key := mcpConnectionCacheKeyWithFeatures(config, openAIForm, userVerification)
	if s.networkPolicyAdvertisedForServer(config) {
		key += "|networkPolicy=1"
	}
	return key
}

func (s *MCPService) mcpHTTPClientCacheKey(config *ServerConfig, openAIForm bool, userVerification bool, sharedHTTPClientKey string) string {
	return s.mcpStdioClientCacheKey(config, openAIForm, userVerification) + "|httpClient=" + sharedHTTPClientKey
}

func mcpHTTPDoerIdentity(client HTTPDoer) string {
	if client == nil {
		return ""
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		if value.IsNil() {
			return ""
		}
		return fmt.Sprintf("%T:%x", client, value.Pointer())
	default:
		return fmt.Sprintf("%T:%v", client, client)
	}
}

func mcpConnectionCacheKey(config *ServerConfig, openAIForm bool) string {
	return mcpConnectionCacheKeyWithFeatures(config, openAIForm, false)
}

func mcpConnectionCacheKeyWithFeatures(config *ServerConfig, openAIForm bool, userVerification bool) string {
	if config == nil {
		return fmt.Sprintf("openaiForm=%t|userVerification=%t", openAIForm, userVerification)
	}
	cloned := cloneServerConfig(config)
	cloned.Enabled = false
	cloned.DisabledReason = ""
	cloned.Required = false
	cloned.StartupReadiness = ""
	cloned.EnabledTools = nil
	cloned.DisabledTools = nil
	cloned.OmitToolsFrom = nil
	cloned.DefaultToolsApprovalMode = nil
	cloned.Tools = nil
	cloned.StartupTimeout = 0
	cloned.ToolTimeout = 0
	applyHTTPRequest := cloned.ApplyHTTPRequest != nil
	cloned.ApplyHTTPRequest = nil
	// Rust #48143: the credential policy is part of the connection identity, so a
	// reconnect cannot reuse a host-credential connection for an executor-only
	// declaration (it is `json:"-"` and therefore not in the marshalled config).
	credentialPolicy := EffectiveCredentialPolicy(config.CredentialPolicy)
	data, err := json.Marshal(cloned)
	if err != nil {
		return fmt.Sprintf("%#v|openaiForm=%t|userVerification=%t|requestAuth=%t|protocolMode=%d|readOnlyTools=%t|credentialPolicy=%s", cloned, openAIForm, userVerification, applyHTTPRequest, config.ProtocolMode, config.RequiresReadOnlyTools, credentialPolicy)
	}
	key := fmt.Sprintf("%s|openaiForm=%t|userVerification=%t|requestAuth=%t|protocolMode=%d|readOnlyTools=%t|credentialPolicy=%s", data, openAIForm, userVerification, applyHTTPRequest, config.ProtocolMode, config.RequiresReadOnlyTools, credentialPolicy)
	for _, reference := range mcpReferencedEnvironmentVariables(config) {
		key += "\x1f" + reference
	}
	return key
}

// mcpReferencedEnvironmentVariables returns the resolved environment values
// that participate in a connection identity (Rust #39930). Remote HTTP header
// variables remain host-sourced. A remote bearer value participates only when
// the host may resolve it; executor-only declarations never read it.
func mcpReferencedEnvironmentVariables(config *ServerConfig) []string {
	if config == nil {
		return nil
	}
	names := make([]string, 0)
	if strings.TrimSpace(config.URL) != "" {
		if envVar := strings.TrimSpace(config.BearerTokenEnvVar); envVar != "" {
			_, hostHasBearer := os.LookupEnv(envVar)
			if EffectiveCredentialPolicy(config.CredentialPolicy) != CredentialPolicyExecutorOnly &&
				(config.IsLocalEnvironment() || hostHasBearer) {
				names = append(names, envVar)
			}
		}
		for _, envVar := range config.EnvHTTPHeaders {
			if envVar = strings.TrimSpace(envVar); envVar != "" {
				names = append(names, envVar)
			}
		}
	} else if strings.TrimSpace(config.Command) != "" {
		for _, envVar := range config.EnvVars {
			if envVar.Source != "remote" && strings.TrimSpace(envVar.Name) != "" {
				names = append(names, strings.TrimSpace(envVar.Name))
			}
		}
	}
	sort.Strings(names)
	references := make([]string, 0, len(names))
	for _, name := range names {
		if len(references) > 0 && strings.HasPrefix(references[len(references)-1], name+"=") {
			continue
		}
		references = append(references, name+"="+os.Getenv(name))
	}
	return references
}

// stdioConnectionLease reports this chat's server-side lease id for a pooled
// stdio server, or an empty string when the helper did not negotiate the
// extension (Rust #57978).
func (s *MCPService) stdioConnectionLease(name string) *mcpChatLease {
	if s == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cached := s.stdioClients[name]
	if cached == nil {
		return nil
	}
	return cached.lease
}

func (s *MCPService) stdioClientForServer(name string, config *ServerConfig) *stdioClient {
	if config == nil {
		return newMCPStdioClient(nil)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(config.Command)
	}
	if s == nil || name == "" {
		client := newMCPStdioClient(config)
		if s != nil {
			client.authChanges = s.authChangeSourceSnapshot()
		}
		return client
	}
	s.mu.Lock()
	if s.stdioClients == nil {
		s.stdioClients = map[string]*cachedMCPStdioClient{}
	}
	openAIForm := s.openAIForm
	userVerification := s.serverAllowsUserVerificationLocked(name)
	authChanges := s.authChangeSource
	key := s.mcpStdioClientCacheKey(config, openAIForm, userVerification)
	if cached := s.stdioClients[name]; cached != nil && cached.key == key && cached.client != nil && !cached.client.isClosed() &&
		(cached.client.isInitialized() || cached.startupTimeout == config.StartupTimeout) {
		s.mu.Unlock()
		return cached.client
	}
	old := s.deleteStdioClientLocked(name)
	var (
		client  *stdioClient
		release func()
		lease   *mcpChatLease
	)
	if s.mcpStdioSharingEligible(name, config) {
		client, lease, release = sharedMCPStdioConnections.acquire(mcpStdioPoolKey(key, config), func() *stdioClient {
			created := newMCPStdioClientWithCapabilities(config, mcpClientFeatures{
				OpenAIForm:       openAIForm,
				UserVerification: userVerification,
				NetworkPolicy:    s.networkPolicyAdvertisedForServer(config),
			})
			created.authChanges = authChanges
			return created
		})
	} else {
		client = newMCPStdioClientWithCapabilities(config, mcpClientFeatures{
			OpenAIForm:       openAIForm,
			UserVerification: userVerification,
			NetworkPolicy:    s.networkPolicyAdvertisedForServer(config),
		})
		client.authChanges = authChanges
	}
	var previous *stdioClient
	if old != nil {
		previous = old.client
	}
	if release == nil && mcpStdioUpgradeFromLegacy(previous, config, openAIForm, userVerification) {
		// A forced reconnect must wait for the previously published helper to
		// exit, but a working legacy helper stays available until the upgrade
		// proves itself: a failed discovery for the same account falls back to
		// it instead of failing the chat (Rust #57210).
		client.legacyFallback = previous
		old = nil
	}
	s.stdioClients[name] = &cachedMCPStdioClient{key: key, client: client, startupTimeout: config.StartupTimeout, release: release, lease: lease}
	s.mu.Unlock()
	closeCachedStdioClients([]*cachedMCPStdioClient{old})
	return client
}

// mcpStdioConnectionFamilyKey identifies the helper a config would launch while
// ignoring the protocol handshake, so an upgrade attempt can recognize the
// legacy connection it replaces.
func mcpStdioConnectionFamilyKey(config *ServerConfig, openAIForm bool, userVerification bool) string {
	if config == nil {
		return ""
	}
	family := config
	if family.ProtocolMode != MCPProtocolLegacy {
		cloned := cloneServerConfig(family)
		cloned.ProtocolMode = MCPProtocolLegacy
		family = &cloned
	}
	return mcpConnectionCacheKeyWithFeatures(family, openAIForm, userVerification)
}

// mcpStdioUpgradeFromLegacy reports whether previous is a ready legacy
// connection for the same helper that config now wants to connect with a
// protocol it has not negotiated yet.
func mcpStdioUpgradeFromLegacy(previous *stdioClient, config *ServerConfig, openAIForm bool, userVerification bool) bool {
	if previous == nil || config == nil {
		return false
	}
	if config.ProtocolMode != MCPProtocolDiscoverLocalStdio && config.ProtocolMode != MCPProtocol20260728 {
		return false
	}
	if previous.legacyDelegate() != nil || previous.isClosed() || !previous.isInitialized() {
		return false
	}
	if previous.protocolModeSnapshot() != MCPProtocolLegacy {
		return false
	}
	return mcpStdioConnectionFamilyKey(previous.config, previous.openAIForm, previous.userVerification) ==
		mcpStdioConnectionFamilyKey(config, openAIForm, userVerification)
}

// authChangeSourceSnapshot returns the configured auth-change source.
func (s *MCPService) authChangeSourceSnapshot() MCPAuthChangeSource {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authChangeSource
}

// SetAuthChangeSource installs the source used to notify opted-in stdio MCP
// servers about credential and ownership changes (Rust #43428). It is a no-op
// when the service is nil. Later stdio connections inherit the current source.
func (s *MCPService) SetAuthChangeSource(source MCPAuthChangeSource) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authChangeSource = source
}

// AuthChangeSource returns the installed auth-change source, or nil.
func (s *MCPService) AuthChangeSource() MCPAuthChangeSource {
	return s.authChangeSourceSnapshot()
}

func (s *MCPService) elicitationHandler() MCPElicitationHandler {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.elicitation
}

// elicitationHandlerForServer restricts user verification to the host-owned
// apps MCP server (Rust #43524). Other registrations cancel verification
// before the client is prompted, even when they reuse the apps server name.
func (s *MCPService) elicitationHandlerForServer(serverName string) MCPElicitationHandler {
	handler := s.elicitationHandler()
	if handler == nil || s.serverAllowsUserVerification(serverName) {
		return handler
	}
	return MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		if IsUserVerificationElicitation(request) {
			return &MCPElicitationResponse{Action: MCPElicitationActionCancel}, nil
		}
		return handler.HandleMCPElicitation(ctx, request)
	})
}

func (s *MCPService) serverAllowsUserVerification(serverName string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverAllowsUserVerificationLocked(serverName)
}

func (s *MCPService) serverAllowsUserVerificationLocked(serverName string) bool {
	return mcpServerAllowsUserVerification(s.servers, serverName, s.userVerification)
}

func mcpServerAllowsUserVerification(servers map[string]MCPServerStatus, serverName string, userVerification bool) bool {
	if !userVerification {
		return false
	}
	status, ok := servers[strings.TrimSpace(serverName)]
	return ok && status.hostOwnedApps
}

// ElicitationHandler returns the currently installed MCP elicitation handler.
func (s *MCPService) ElicitationHandler() MCPElicitationHandler {
	return s.elicitationHandler()
}

func (s *MCPService) progressHandler() MCPProgressHandler {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.progress
}

func (s *MCPService) rootsForThread(threadID string) []MCPRoot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	provider := s.roots
	s.mu.Unlock()
	if provider == nil {
		return nil
	}
	return cloneMCPRoots(provider.MCPRoots(threadID))
}

func threadIDFromResourceReadParams(params *MCPResourceReadParams) string {
	if params == nil || params.ThreadID == nil {
		return ""
	}
	return strings.TrimSpace(*params.ThreadID)
}

func (s *MCPService) oauthLoginCompletionHandler() MCPOAuthLoginCompletionHandler {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.oauthComplete
}

func (s *MCPService) requiredServerAvailable(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.required == nil || !s.required[name] {
		return nil
	}
	config, configOK := s.configs[name]
	if !configOK || !config.Enabled {
		return invalidMCPRequest(fmt.Sprintf("required MCP server %q is not enabled", name))
	}
	status, statusOK := s.servers[name]
	if !statusOK {
		return invalidMCPRequest(fmt.Sprintf("required MCP server %q is missing", name))
	}
	switch status.State {
	case "", MCPServerReady, MCPServerStarting:
		return nil
	case MCPServerFailed, MCPServerCancelled, MCPServerStopped:
		reason := ""
		if status.Error != nil {
			reason = strings.TrimSpace(*status.Error)
		}
		if reason == "" {
			reason = string(status.State)
		}
		return invalidMCPRequest(fmt.Sprintf("required MCP server %q is unavailable: %s", name, reason))
	default:
		return nil
	}
}

func (s *MCPService) authStatusForConfig(name string, config *ServerConfig) MCPAuthStatus {
	if config == nil || strings.TrimSpace(config.URL) == "" {
		return MCPAuthUnsupported
	}
	if config.EffectiveAuth() == ServerAuthEMAAuth {
		// Rust #55334: enterprise authorization is reported like OAuth once the
		// account-scoped IdP grant exists, and as not signed in before that.
		if !config.EMAConfigured {
			return MCPAuthUnsupported
		}
		if config.EMACredentialsAvailable {
			return MCPAuthOAuth
		}
		return MCPAuthNotLoggedIn
	}
	if strings.TrimSpace(config.BearerTokenEnvVar) != "" {
		return MCPAuthBearerToken
	}
	if config.EffectiveAuth() == ServerAuthChatGPT && !config.IsLocalEnvironment() {
		if config.SafeRemoteChatGPTAuthorization() {
			return MCPAuthBearerToken
		}
		return MCPAuthUnsupported
	}
	if config.ApplyHTTPRequest != nil || configuredAuthorizationHeader(config) {
		return MCPAuthBearerToken
	}
	store := s.oauth
	if store == nil && strings.TrimSpace(config.CodexHome) != "" {
		store = NewOAuthStoreWithMode(config.CodexHome, config.OAuthCredentialsStoreMode)
	}
	if store == nil {
		if strings.TrimSpace(config.OAuthClientID) != "" || strings.TrimSpace(config.OAuthResource) != "" {
			return MCPAuthNotLoggedIn
		}
		return MCPAuthUnsupported
	}
	serverName := strings.TrimSpace(config.OAuthServerName)
	if serverName == "" {
		serverName = name
	}
	status, err := store.AuthStatus(serverName, config)
	if err != nil {
		return MCPAuthUnknown
	}
	return status
}

func configuredAuthorizationHeader(config *ServerConfig) bool {
	if config == nil {
		return false
	}
	for name, value := range config.HTTPHeaders {
		if strings.EqualFold(strings.TrimSpace(name), "Authorization") && strings.TrimSpace(value) != "" {
			return true
		}
	}
	for name, envVar := range config.EnvHTTPHeaders {
		if strings.EqualFold(strings.TrimSpace(name), "Authorization") && strings.TrimSpace(os.Getenv(strings.TrimSpace(envVar))) != "" {
			return true
		}
	}
	return false
}

func (s *MCPService) readCachedResource(key *MCPResourceCacheKey) (*MCPResourceReadResponse, bool) {
	if s == nil || s.resourceCache == nil {
		return nil, false
	}
	return s.resourceCache.Read(key)
}

func (s *MCPService) writeCachedResource(key *MCPResourceCacheKey, response *MCPResourceReadResponse) {
	if s == nil || s.resourceCache == nil {
		return
	}
	s.resourceCache.Write(key, response)
}

func (s *MCPService) clearResourceCache() {
	if s == nil || s.resourceCache == nil {
		return
	}
	s.resourceCache.Clear()
}

func (s *MCPService) clearHTTPClients() {
	if s == nil {
		return
	}
	s.mu.Lock()
	clients := s.httpClientsForCloseLocked()
	s.httpClients = map[string]*cachedMCPHTTPClient{}
	s.mu.Unlock()
	closeHTTPClients(clients)
}

func (s *MCPService) httpClientsForCloseLocked() []*httpClient {
	if s == nil || len(s.httpClients) == 0 {
		return nil
	}
	clients := make([]*httpClient, 0, len(s.httpClients))
	for _, cached := range s.httpClients {
		if cached != nil && cached.client != nil {
			clients = append(clients, cached.client)
		}
	}
	return clients
}

func (s *MCPService) deleteHTTPClientLocked(name string) *httpClient {
	if s == nil || s.httpClients == nil {
		return nil
	}
	cached := s.httpClients[name]
	delete(s.httpClients, name)
	if cached == nil {
		return nil
	}
	return cached.client
}

func closeHTTPClients(clients []*httpClient) {
	for _, client := range clients {
		if client != nil {
			_ = client.Close()
		}
	}
}

func (s *MCPService) clearStdioClients() {
	if s == nil {
		return
	}
	s.mu.Lock()
	clients := s.stdioClientsForCloseLocked()
	s.stdioClients = map[string]*cachedMCPStdioClient{}
	s.mu.Unlock()
	retireCachedStdioClients(clients)
}

func (s *MCPService) stdioClientsForCloseLocked() []*cachedMCPStdioClient {
	if s == nil || len(s.stdioClients) == 0 {
		return nil
	}
	clients := make([]*cachedMCPStdioClient, 0, len(s.stdioClients))
	for _, cached := range s.stdioClients {
		if cached != nil && cached.client != nil {
			clients = append(clients, cached)
		}
	}
	return clients
}

func (s *MCPService) deleteStdioClientLocked(name string) *cachedMCPStdioClient {
	if s == nil || s.stdioClients == nil {
		return nil
	}
	cached := s.stdioClients[name]
	delete(s.stdioClients, name)
	return cached
}

// closeCachedStdioClients releases pooled connections and closes private ones.
// Releasing a shared connection keeps the helper running for other chats.
func closeCachedStdioClients(cached []*cachedMCPStdioClient) {
	for _, entry := range cached {
		if entry == nil {
			continue
		}
		if entry.release != nil {
			entry.release()
			entry.release = nil
			continue
		}
		closeStdioClients([]*stdioClient{entry.client})
	}
}

// retireCachedStdioClients retires pooled connections before releasing them:
// a forced refresh must start a fresh helper for new chats, while chats that
// already share the old helper keep it until their own release (Rust #57211).
func retireCachedStdioClients(cached []*cachedMCPStdioClient) {
	for _, entry := range cached {
		if entry == nil || entry.release == nil || entry.client == nil {
			continue
		}
		sharedMCPStdioConnections.retireClient(entry.client)
	}
	closeCachedStdioClients(cached)
}

func closeStdioClients(clients []*stdioClient) {
	for _, client := range clients {
		if client == nil {
			continue
		}
		if fallback := client.takeLegacyFallback(); fallback != nil {
			_ = fallback.Close()
		}
		_ = client.Close()
	}
}

func (s *MCPService) oauthLoginsForCancelLocked() []*OAuthLoginServer {
	if s == nil || len(s.oauthLogins) == 0 {
		return nil
	}
	logins := make([]*OAuthLoginServer, 0, len(s.oauthLogins))
	for _, login := range s.oauthLogins {
		if login != nil {
			logins = append(logins, login)
		}
	}
	return logins
}

func (s *MCPService) deleteOAuthLoginLocked(name string) *OAuthLoginServer {
	if s == nil || s.oauthLogins == nil {
		return nil
	}
	login := s.oauthLogins[name]
	delete(s.oauthLogins, name)
	return login
}

func cancelOAuthLogins(logins []*OAuthLoginServer) {
	for _, login := range logins {
		if login == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = login.Cancel(ctx)
		cancel()
	}
}

func cloneMCPServerStatus(status MCPServerStatus) MCPServerStatus {
	status.RuntimeStatus = cloneMCPConnectionStatus(status.RuntimeStatus)
	status.PluginID = cloneStringPtr(status.PluginID)
	status.ServerCapabilities = cloneMCPRawMessage(status.ServerCapabilities)
	status.ToolListMeta = cloneMCPRawMessage(status.ToolListMeta)
	status.FailureReason = cloneStringPtr(status.FailureReason)
	status.Server.Args = append([]string(nil), status.Server.Args...)
	status.Server.Icons = append([]any(nil), status.Server.Icons...)
	if status.Server.Title != nil {
		status.Server.Title = cloneStringPtr(status.Server.Title)
	}
	if status.Server.Description != nil {
		status.Server.Description = cloneStringPtr(status.Server.Description)
	}
	if status.Server.WebsiteURL != nil {
		status.Server.WebsiteURL = cloneStringPtr(status.Server.WebsiteURL)
	}
	if status.ServerInfo != nil {
		info := *status.ServerInfo
		info.Args = append([]string(nil), info.Args...)
		info.Icons = append([]any(nil), info.Icons...)
		info.Title = cloneStringPtr(info.Title)
		info.Description = cloneStringPtr(info.Description)
		info.WebsiteURL = cloneStringPtr(info.WebsiteURL)
		status.ServerInfo = &info
	}
	status.Tools = append([]MCPToolInfo(nil), status.Tools...)
	status.Resources = append([]MCPResource(nil), status.Resources...)
	status.ResourceTemplates = append([]MCPResourceTemplate(nil), status.ResourceTemplates...)
	if status.Error != nil {
		value := *status.Error
		status.Error = &value
	}
	if status.ToolsError != nil {
		value := *status.ToolsError
		status.ToolsError = &value
	}
	for i := range status.Tools {
		if status.Tools[i].InputSchema != nil {
			schema := make(map[string]any, len(status.Tools[i].InputSchema))
			for key, value := range status.Tools[i].InputSchema {
				schema[key] = value
			}
			status.Tools[i].InputSchema = schema
		}
		status.Tools[i].OutputSchema = cloneJSONValue(status.Tools[i].OutputSchema)
		status.Tools[i].Annotations = cloneJSONValue(status.Tools[i].Annotations)
		status.Tools[i].Icons = append([]any(nil), status.Tools[i].Icons...)
		status.Tools[i].Meta = cloneJSONValue(status.Tools[i].Meta)
	}
	for i := range status.Resources {
		status.Resources[i].Annotations = cloneJSONValue(status.Resources[i].Annotations)
		status.Resources[i].Icons = append([]any(nil), status.Resources[i].Icons...)
		status.Resources[i].Meta = cloneJSONValue(status.Resources[i].Meta)
	}
	for i := range status.ResourceTemplates {
		status.ResourceTemplates[i].Annotations = cloneJSONValue(status.ResourceTemplates[i].Annotations)
		status.ResourceTemplates[i].Meta = cloneJSONValue(status.ResourceTemplates[i].Meta)
	}
	return status
}

func (s *MCPServerStatus) effectiveName() string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(s.Name) != "" {
		return s.Name
	}
	return s.Server.Name
}

// mcpServerNameFilter returns the trimmed `serverName` selection of a status
// list request, or "" when the request wants the full inventory (Rust #48783).
func mcpServerNameFilter(params *MCPListServerStatusParams) string {
	if params == nil || params.ServerName == nil {
		return ""
	}
	return strings.TrimSpace(*params.ServerName)
}

func paginateMCPStatuses(statuses []MCPServerStatus, params *MCPListServerStatusParams) ([]MCPServerStatus, *string, error) {
	total := len(statuses)
	start := 0
	if params != nil && params.Cursor != nil {
		cursor := strings.TrimSpace(*params.Cursor)
		if cursor == "" {
			return nil, nil, invalidMCPRequest(fmt.Sprintf("invalid cursor: %s", *params.Cursor))
		}
		idx, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return nil, nil, invalidMCPRequest(fmt.Sprintf("invalid cursor: %s", cursor))
		}
		if idx > uint64(total) {
			return nil, nil, invalidMCPRequest(fmt.Sprintf("cursor %d exceeds total MCP servers %d", idx, total))
		}
		start = int(idx)
	}
	if start < 0 || start > total {
		return nil, nil, invalidMCPRequest(fmt.Sprintf("cursor %d exceeds total MCP servers %d", start, total))
	}
	limit := total
	if params != nil && params.Limit != nil {
		limit = int(*params.Limit)
		if limit < 1 {
			limit = 1
		}
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := append([]MCPServerStatus(nil), statuses[start:end]...)
	if end < total {
		next := strconv.Itoa(end)
		return page, &next, nil
	}
	return page, nil, nil
}

func toolMapFromList(tools []MCPToolInfo) map[string]MCPToolInfo {
	out := map[string]MCPToolInfo{}
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		out[tool.Name] = tool
	}
	return out
}

type mcpServerStatusResource struct {
	Annotations any    `json:"annotations,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Name        string `json:"name"`
	Size        *int64 `json:"size,omitempty"`
	Title       string `json:"title,omitempty"`
	URI         string `json:"uri"`
	Icons       []any  `json:"icons,omitempty"`
	Meta        any    `json:"_meta,omitempty"`
}

type mcpServerStatusResourceTemplate struct {
	Annotations any    `json:"annotations,omitempty"`
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

func mcpServerStatusResources(resources []MCPResource) []mcpServerStatusResource {
	if resources == nil {
		return []mcpServerStatusResource{}
	}
	out := make([]mcpServerStatusResource, 0, len(resources))
	for _, resource := range resources {
		out = append(out, mcpServerStatusResource{
			Annotations: cloneJSONValue(resource.Annotations),
			Description: resource.Description,
			MimeType:    resource.MimeType,
			Name:        resource.Name,
			Size:        cloneInt64PtrMCP(resource.Size),
			Title:       resource.Title,
			URI:         resource.URI,
			Icons:       cloneJSONSlice(resource.Icons),
			Meta:        cloneJSONValue(resource.Meta),
		})
	}
	return out
}

func mcpServerStatusResourceTemplates(templates []MCPResourceTemplate) []mcpServerStatusResourceTemplate {
	if templates == nil {
		return []mcpServerStatusResourceTemplate{}
	}
	out := make([]mcpServerStatusResourceTemplate, 0, len(templates))
	for _, template := range templates {
		out = append(out, mcpServerStatusResourceTemplate{
			Annotations: cloneJSONValue(template.Annotations),
			URITemplate: template.URITemplate,
			Name:        template.Name,
			Title:       template.Title,
			Description: template.Description,
			MimeType:    template.MimeType,
		})
	}
	return out
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneMCPConnectionStatus(value *MCPConnectionStatus) *MCPConnectionStatus {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUint64PtrMCP(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64PtrMCP(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}

func firstNonEmptyMCP(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringFromAnyMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
