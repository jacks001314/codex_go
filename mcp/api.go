package mcp

import (
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
)

var ErrInvalidMCPRequest = errors.New("invalid mcp request")

const mcpToolThreadIDMetaKey = "threadId"
const mcpSandboxStateMetaCapability = "codex/sandbox-state-meta"

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

type MCPStartupObserver func(name string, status MCPServerStartupState, err error)

type MCPAuthStatus string

const (
	MCPAuthUnknown     MCPAuthStatus = "unknown"
	MCPAuthUnsupported MCPAuthStatus = "unsupported"
	MCPAuthNotLoggedIn MCPAuthStatus = "notLoggedIn"
	MCPAuthBearerToken MCPAuthStatus = "bearerToken"
	MCPAuthOAuth       MCPAuthStatus = "oAuth"
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

	// Turn-scoped catalog capture can stop waiting for optional startup after a
	// shared grace period. Management RPCs leave NonBlockingOptional false.
	RequiredServers      []string      `json:"-"`
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
	// PluginID reports the owning plugin for plugin-contributed servers and is
	// nil for servers from other sources (Rust 78d3665d15).
	PluginID          *string               `json:"pluginId,omitempty"`
	ServerInfo        *MCPServerInfo        `json:"serverInfo,omitempty"`
	Tools             []MCPToolInfo         `json:"tools,omitempty"`
	Resources         []MCPResource         `json:"resources,omitempty"`
	ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates,omitempty"`
	AuthStatus        MCPAuthStatus         `json:"authStatus,omitempty"`
	Server            MCPServerInfo         `json:"server"`
	State             MCPServerStartupState `json:"state"`
	Error             *string               `json:"error,omitempty"`
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
		Name              string                            `json:"name"`
		PluginID          *string                           `json:"pluginId"`
		ServerInfo        *MCPServerInfo                    `json:"serverInfo"`
		Tools             map[string]MCPToolInfo            `json:"tools"`
		Resources         []mcpServerStatusResource         `json:"resources"`
		ResourceTemplates []mcpServerStatusResourceTemplate `json:"resourceTemplates"`
		AuthStatus        MCPAuthStatus                     `json:"authStatus"`
		State             MCPServerStartupState             `json:"state"`
		Error             *string                           `json:"error,omitempty"`
	}{
		Name:              name,
		PluginID:          cloneStringPtr(s.PluginID),
		ServerInfo:        serverInfo,
		Tools:             toolMapFromList(s.Tools),
		Resources:         mcpServerStatusResources(s.Resources),
		ResourceTemplates: mcpServerStatusResourceTemplates(s.ResourceTemplates),
		AuthStatus:        authStatus,
		State:             state,
		Error:             cloneStringPtr(s.Error),
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
}

func (r *MCPServerOauthLoginResponse) MarshalJSON() ([]byte, error) {
	authorizationURL := r.AuthorizationURL
	if authorizationURL == "" {
		authorizationURL = r.URL
	}
	return json.Marshal(struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}{
		AuthorizationURL: authorizationURL,
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
}

func (p MCPResourceReadParams) MarshalJSON() ([]byte, error) {
	type payload struct {
		ThreadID *string `json:"threadId,omitempty"`
		Server   string  `json:"server"`
		URI      string  `json:"uri"`
	}
	return json.Marshal(payload{
		ThreadID: cloneStringPtr(p.ThreadID),
		Server:   firstNonEmptyMCP(p.Server, p.ServerName),
		URI:      p.URI,
	})
}

type MCPResourceReadResponse struct {
	Contents []MCPResourceContent `json:"contents"`
}

func (r *MCPResourceReadResponse) MarshalJSON() ([]byte, error) {
	contents := append([]MCPResourceContent(nil), r.Contents...)
	if contents == nil {
		contents = []MCPResourceContent{}
	}
	return json.Marshal(struct {
		Contents []MCPResourceContent `json:"contents"`
	}{Contents: contents})
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
	ThreadID   string `json:"threadId,omitempty"`
	TurnID     string `json:"turnId,omitempty"`
	ItemID     string `json:"itemId,omitempty"`
	Server     string `json:"server,omitempty"`
	Tool       string `json:"tool,omitempty"`
	ServerName string `json:"serverName,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	Arguments  any    `json:"arguments,omitempty"`
	Meta       any    `json:"_meta,omitempty"`
	CallID     string `json:"-"`

	PermissionProfile        string `json:"-"`
	SandboxCWD               string `json:"-"`
	CodexLinuxSandboxExe     string `json:"-"`
	UseLegacyLandlock        bool   `json:"-"`
	ServerEnvironmentID      string `json:"-"`
	SupportsSandboxStateMeta bool   `json:"-"`
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
	mu                  sync.Mutex
	servers             map[string]MCPServerStatus
	configs             map[string]ServerConfig
	dynamicConfig       map[string]bool
	required            map[string]bool
	starting            map[string]int
	httpClients         map[string]*cachedMCPHTTPClient
	stdioClients        map[string]*cachedMCPStdioClient
	oauthLogins         map[string]*OAuthLoginServer
	oauth               *OAuthStore
	resourceCache       *MCPResourceCache
	elicitation         MCPElicitationHandler
	progress            MCPProgressHandler
	roots               MCPRootsProvider
	oauthComplete       MCPOAuthLoginCompletionHandler
	openAIForm          bool
	generation          uint64
	sharedHTTPClient    HTTPDoer
	sharedHTTPClientKey string
}

var sharedOptionalMCPStartupGrace = struct {
	sync.Mutex
	deadlines map[string]time.Time
}{deadlines: map[string]time.Time{}}

type cachedMCPHTTPClient struct {
	key    string
	client *httpClient
}

type cachedMCPStdioClient struct {
	key    string
	client *stdioClient
}

func NewMCPService(runtime *RuntimeConfig) *MCPService {
	service := &MCPService{servers: map[string]MCPServerStatus{}, configs: map[string]ServerConfig{}, dynamicConfig: map[string]bool{}, required: map[string]bool{}, starting: map[string]int{}, httpClients: map[string]*cachedMCPHTTPClient{}, stdioClients: map[string]*cachedMCPStdioClient{}, oauthLogins: map[string]*OAuthLoginServer{}, resourceCache: NewMCPResourceCache(nil), generation: 1}
	if runtime != nil {
		service.sharedHTTPClient = runtime.HTTPClient
		service.sharedHTTPClientKey = mcpHTTPDoerIdentity(runtime.HTTPClient)
		if strings.TrimSpace(runtime.CodexHome) != "" {
			service.oauth = NewOAuthStore(runtime.CodexHome)
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
			config.ProtocolMode = runtime.ProtocolMode
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
				continue
			}
			service.configs[name] = config
			authStatus := service.authStatusForConfig(name, &config)
			var pluginID *string
			if strings.TrimSpace(registration.PluginID) != "" {
				pluginID = cloneStringPtr(&registration.PluginID)
			}
			service.servers[name] = MCPServerStatus{
				Name:       name,
				PluginID:   pluginID,
				Server:     info,
				ServerInfo: &info,
				State:      MCPServerReady,
				AuthStatus: authStatus,
			}
		}
	}
	return service
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
	var oldStdioClients []*stdioClient
	for name, cached := range s.httpClients {
		config, ok := refreshed.configs[name]
		if ok && cached != nil && cached.client != nil && !cached.client.isClosed() &&
			cached.key == mcpHTTPConnectionCacheKey(&config, s.openAIForm, refreshed.sharedHTTPClientKey) {
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
			cached.key == mcpConnectionCacheKey(&config, s.openAIForm) {
			nextStdioClients[name] = cached
			preserveMCPServerInventory(refreshed.servers, s.servers, name)
			continue
		}
		if cached != nil && cached.client != nil {
			oldStdioClients = append(oldStdioClients, cached.client)
		}
	}
	oldOAuthLogins := s.oauthLoginsForCancelLocked()
	s.servers = refreshed.servers
	s.configs = refreshed.configs
	s.dynamicConfig = refreshed.dynamicConfig
	s.required = refreshed.required
	s.starting = refreshed.starting
	s.httpClients = nextHTTPClients
	s.stdioClients = nextStdioClients
	s.oauthLogins = map[string]*OAuthLoginServer{}
	s.oauth = refreshed.oauth
	s.sharedHTTPClient = refreshed.sharedHTTPClient
	s.sharedHTTPClientKey = refreshed.sharedHTTPClientKey
	s.generation++
	if s.resourceCache == nil {
		s.resourceCache = NewMCPResourceCache(nil)
	}
	s.mu.Unlock()
	closeHTTPClients(oldHTTPClients)
	closeStdioClients(oldStdioClients)
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
	current.Tools = append([]MCPToolInfo(nil), old.Tools...)
	current.Resources = append([]MCPResource(nil), old.Resources...)
	current.ResourceTemplates = append([]MCPResourceTemplate(nil), old.ResourceTemplates...)
	next[name] = current
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
	closeStdioClients(stdioClients)
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
		delete(s.dynamicConfig, name)
		delete(s.required, name)
		delete(s.servers, name)
		s.clearResourceCache()
		s.mu.Unlock()
		closeHTTPClients([]*httpClient{oldHTTPClient})
		closeStdioClients([]*stdioClient{oldStdioClient})
		cancelOAuthLogins([]*OAuthLoginServer{oldOAuthLogin})
		return
	}
	if !config.Enabled {
		delete(s.configs, name)
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
		closeStdioClients([]*stdioClient{oldStdioClient})
		cancelOAuthLogins([]*OAuthLoginServer{oldOAuthLogin})
		return
	}
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
	closeStdioClients([]*stdioClient{oldStdioClient})
	cancelOAuthLogins([]*OAuthLoginServer{oldOAuthLogin})
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

func (s *MCPService) SetOpenAIFormElicitationEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.openAIForm == enabled {
		s.mu.Unlock()
		return
	}
	s.openAIForm = enabled
	s.generation++
	oldHTTPClients := s.httpClientsForCloseLocked()
	oldStdioClients := s.stdioClientsForCloseLocked()
	s.httpClients = map[string]*cachedMCPHTTPClient{}
	s.stdioClients = map[string]*cachedMCPStdioClient{}
	s.mu.Unlock()
	closeHTTPClients(oldHTTPClients)
	closeStdioClients(oldStdioClients)
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
		servers = append(servers, cloned)
	}
	s.mu.Unlock()
	threadID := ""
	if params != nil && params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
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

type mcpInventoryStatusResult struct {
	Index  int
	Status MCPServerStatus
	Err    error
}

func (s *MCPService) populateStatusInventories(params *MCPListServerStatusParams, detail *MCPServerStatusDetail, observer MCPStartupObserver, servers []MCPServerStatus, configs map[string]ServerConfig, dynamicConfigs map[string]bool, threadID string) []MCPServerStatus {
	if !detail.includesTools() {
		return servers
	}
	resultCh := make(chan mcpInventoryStatusResult, len(servers))
	pending := make(map[int]string, len(servers))
	required := map[string]bool{}
	if params != nil {
		for _, name := range params.RequiredServers {
			if name = strings.TrimSpace(name); name != "" {
				required[name] = true
			}
		}
	}
	requiredIndexes := map[int]bool{}
	for i := range servers {
		name := servers[i].effectiveName()
		config, ok := configs[name]
		if !ok {
			continue
		}
		if dynamicConfigs[name] && (params == nil || params.Detail == nil) {
			notifyMCPStartupObserver(observer, name, servers[i].State, nil)
			continue
		}
		// Rust #38217: a required server with cached tool definitions may stay
		// dormant under the lazy startup policy. The cached tools satisfy
		// catalog capture until one of the server's tools is called.
		if params != nil && params.NonBlockingOptional &&
			servers[i].State == MCPServerReady && len(servers[i].Tools) > 0 &&
			(config.Required || required[name]) {
			notifyMCPStartupObserver(observer, name, MCPServerReady, nil)
			continue
		}
		notifyMCPStartupObserver(observer, name, MCPServerStarting, nil)
		pending[i] = name
		if config.Required || required[name] || (IsCodexAppsMCPServerName(name) && len(servers[i].Tools) == 0) {
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
			notifyMCPStartupObserver(observer, name, MCPServerFailed, result.Err)
		} else {
			notifyMCPStartupObserver(observer, name, result.Status.State, nil)
		}
	}
	if params == nil || !params.NonBlockingOptional {
		for len(pending) > 0 {
			applyResult(<-resultCh)
		}
		return servers
	}

	grace := params.OptionalStartupGrace
	if grace <= 0 {
		grace = time.Second
	}
	defaultDeadline := time.Now().Add(grace)
	optionalDeadlines := map[int]time.Time{}
	for index, name := range pending {
		if !requiredIndexes[index] {
			config := configs[name]
			agentPlugin := servers[index].PluginID != nil
			oauthResolved := s.authStatusForConfig(name, &config) == MCPAuthOAuth
			if key, eligible := mcpToolCatalogGraceKey(name, &config, s.openAIForm, agentPlugin, oauthResolved); eligible {
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
	return servers
}

func (s *MCPService) inventoryStatusForConfig(index int, name string, config *ServerConfig, status MCPServerStatus, includeInventory bool, threadID string) mcpInventoryStatusResult {
	inventory, err := s.listInventoryForConfig(name, config, threadID)
	if err == nil {
		status.Tools = inventory.Tools
		if includeInventory {
			status.Resources = inventory.Resources
			status.ResourceTemplates = inventory.ResourceTemplates
		}
		status.State = MCPServerReady
		status.Error = nil
	} else if isRunnableMCPConfig(config) {
		message := err.Error()
		status.State = MCPServerFailed
		status.Error = &message
		status.Tools = nil
		status.Resources = nil
		status.ResourceTemplates = nil
	}
	status.AuthStatus = s.authStatusForConfig(status.effectiveName(), config)
	s.recordInventoryStatus(status.effectiveName(), status, true, includeInventory)
	return mcpInventoryStatusResult{Index: index, Status: status, Err: err}
}

func notifyMCPStartupObserver(observer MCPStartupObserver, name string, status MCPServerStartupState, err error) {
	if observer != nil {
		observer(name, status, err)
	}
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
		if key, eligible := mcpToolCatalogGraceKey(name, &config, s.openAIForm, status.PluginID != nil, oauthResolved); eligible {
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
	if config == nil {
		return strings.TrimSpace(name) + "\x00" + mcpConnectionCacheKey(config, openAIForm), true
	}
	if strings.TrimSpace(config.Command) != "" {
		return strings.TrimSpace(name) + "\x00" + mcpConnectionCacheKey(config, openAIForm), true
	}
	if strings.TrimSpace(config.URL) == "" {
		return "", false
	}
	if !mcpToolCatalogCacheEligible(config, oauthCredentialsResolved) {
		return "", false
	}
	key := mcpConnectionCacheKey(config, openAIForm)
	envNames := make([]string, 0, 1+len(config.EnvHTTPHeaders))
	if envVar := strings.TrimSpace(config.BearerTokenEnvVar); envVar != "" {
		envNames = append(envNames, envVar)
	}
	for _, envVar := range config.EnvHTTPHeaders {
		if envVar = strings.TrimSpace(envVar); envVar != "" {
			envNames = append(envNames, envVar)
		}
	}
	sort.Strings(envNames)
	for _, envName := range envNames {
		key += "\x1f" + envName + "=" + os.Getenv(envName)
	}
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
	url := "http://localhost/oauth/" + name
	if config, ok := s.serverConfig(name); ok && strings.TrimSpace(config.URL) != "" {
		if config.EffectiveAuth() == ServerAuthChatGPT && !config.IsLocalEnvironment() {
			return nil, invalidMCPRequest("OAuth login is not supported for executor-owned ChatGPT MCP servers")
		}
		if loginURL, ok := s.startOAuthLoginServer(name, &config, params); ok {
			url = loginURL
		} else if registration == MCPServerOauthClientRegistrationDcr && strings.TrimSpace(config.OAuthClientID) == "" {
			// Rust 6dc3ac8721: forced DCR must not silently fall back to a
			// non-registered authorize URL when the server cannot register.
			return nil, invalidMCPRequest("MCP OAuth login requires dynamic client registration (clientRegistration=dcr), but the server does not advertise a registration endpoint")
		} else {
			client := s.httpClientForServer(name, &config).oauthHTTPClient(mcpOAuthLoginDiscoveryTimeout(params.TimeoutSecs))
			url = buildMCPOAuthURLForLogin(&config, params.Scopes, params.TimeoutSecs, client)
		}
	}
	return &MCPServerOauthLoginResponse{AuthorizationURL: url, URL: url}, nil
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

func (s *MCPService) startOAuthLoginServer(name string, config *ServerConfig, params *MCPServerOauthLoginParams) (string, bool) {
	if s == nil || config == nil || params == nil || strings.TrimSpace(config.URL) == "" {
		return "", false
	}
	registration, err := mcpServerOauthClientRegistration(params)
	if err != nil {
		return "", false
	}
	store := s.oauthStoreForConfig(config)
	if store == nil {
		return "", false
	}
	callbackTimeout := mcpOAuthLoginTimeout(params.TimeoutSecs)
	client := s.httpClientForServer(name, config).oauthHTTPClient(0)
	ctx := context.Background()
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, config.URL, client)
	if err != nil || discovery == nil || strings.TrimSpace(discovery.AuthorizationEndpoint) == "" || strings.TrimSpace(discovery.TokenEndpoint) == "" {
		return "", false
	}
	clientID := strings.TrimSpace(config.OAuthClientID)
	if clientID == "" && strings.TrimSpace(discovery.RegistrationEndpoint) == "" {
		return "", false
	}
	login, err := StartOAuthLoginServer(ctx, &OAuthLoginServerOptions{
		ServerName:            config.OAuthCredentialName(name),
		ServerURL:             config.URL,
		ClientID:              clientID,
		RegistrationEndpoint:  discovery.RegistrationEndpoint,
		AuthorizationEndpoint: discovery.AuthorizationEndpoint,
		TokenEndpoint:         discovery.TokenEndpoint,
		Resource:              firstNonEmptyMCP(config.OAuthResource, discovery.Resource),
		Scopes:                params.Scopes,
		Store:                 store,
		HTTPClient:            client,
		Timeout:               callbackTimeout,
		ClientRegistration:    registration,
		CIMDAdvertised:        &discovery.ClientIDMetadataDocumentSupported,
		PublicClientAuth:      &discovery.PublicClientTokenAuthSupported,
	})
	if err != nil {
		return "", false
	}
	threadID := ""
	if params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
	}
	s.trackOAuthLogin(name, threadID, login)
	return login.AuthorizationURL, true
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
		return NewOAuthStore(home)
	}
	return nil
}

func (s *MCPService) trackOAuthLogin(name string, threadID string, login *OAuthLoginServer) {
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
		s.notifyOAuthLoginCompleted(name, threadID, result)
	}()
}

func (s *MCPService) notifyOAuthLoginCompleted(name string, threadID string, result *OAuthLoginServerResult) {
	handler := s.oauthLoginCompletionHandler()
	if handler == nil {
		return
	}
	completion := &MCPOAuthLoginCompletion{
		Name:     name,
		ThreadID: threadID,
		Success:  result != nil && result.Error == nil,
	}
	if result != nil && result.Error != nil {
		completion.Error = result.Error.Error()
	}
	handler.HandleMCPOAuthLoginCompleted(context.Background(), normalizeMCPOAuthLoginCompletion(completion))
}

func (s *MCPService) Refresh() *MCPServerRefreshResponse {
	if s != nil {
		s.mu.Lock()
		s.generation++
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
	if err := s.requiredServerAvailable(server); err != nil {
		return nil, err
	}
	threadID := threadIDFromResourceReadParams(params)
	roots := s.rootsForThread(threadID)
	cacheKey := &MCPResourceCacheKey{Server: server, URI: uri, ThreadID: threadID, RootsKey: mcpRootsCacheKey(roots)}
	if cached, ok := s.readCachedResource(cacheKey); ok {
		return cached, nil
	}
	request := *params
	request.Server = server
	request.ServerName = ""
	request.URI = uri
	var response *MCPResourceReadResponse
	var err error
	if config, ok := s.serverConfig(server); ok {
		if strings.TrimSpace(config.URL) != "" {
			if err := ValidateServerAuth(server, &config); err != nil {
				return nil, err
			}
			response, err = readMCPHTTPResourceWithClient(s.httpClientForServer(server, &config), server, roots, s.elicitationHandler(), s.progressHandler(), &request)
			if err != nil {
				return nil, err
			}
			s.writeCachedResource(cacheKey, response)
			return response, nil
		}
		if strings.TrimSpace(config.Command) != "" {
			response, err = readMCPStdioResourceWithClient(s.stdioClientForServer(server, &config), server, roots, s.elicitationHandler(), s.progressHandler(), &request)
			if err != nil {
				return nil, err
			}
			s.writeCachedResource(cacheKey, response)
			return response, nil
		}
	}
	response = &MCPResourceReadResponse{Contents: []MCPResourceContent{{URI: uri, MimeType: "text/plain", Text: ""}}}
	s.writeCachedResource(cacheKey, response)
	return response, nil
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
	meta := s.augmentToolCallMeta(params)
	roots := s.rootsForThread(params.ThreadID)
	if config, ok := s.serverConfig(server); ok {
		if strings.TrimSpace(config.URL) != "" {
			if err := ValidateServerAuth(server, &config); err != nil {
				return nil, err
			}
			return callMCPHTTPToolWithClient(s.httpClientForServer(server, &config), server, params.ThreadID, params.TurnID, params.ItemID, roots, s.elicitationHandler(), s.progressHandler(), tool, params.Arguments, meta)
		}
		if strings.TrimSpace(config.Command) != "" {
			return callMCPStdioToolWithClient(s.stdioClientForServer(server, &config), server, params.ThreadID, params.TurnID, params.ItemID, roots, s.elicitationHandler(), s.progressHandler(), tool, params.Arguments, meta)
		}
	}
	encoded, err := json.Marshal(params.Arguments)
	if err != nil {
		return nil, err
	}
	return &MCPToolCallResponse{Content: []MCPToolCallContent{{Type: "text", Text: string(encoded)}}}, nil
}

func (s *MCPService) augmentToolCallMeta(params *MCPToolCallParams) any {
	meta := mcpToolCallMetaWithThreadID(params.Meta, params.ThreadID)
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
		return meta
	}
	if strings.TrimSpace(params.SandboxCWD) == "" {
		return meta
	}
	var useLegacyLandlock *bool
	if params.UseLegacyLandlock {
		val := true
		useLegacyLandlock = &val
	}
	sandboxState := &SandboxState{
		PermissionProfile:    params.PermissionProfile,
		SandboxCWD:           params.SandboxCWD,
		CodexLinuxSandboxExe: params.CodexLinuxSandboxExe,
		UseLegacyLandlock:    useLegacyLandlock,
	}
	stateValue, err := json.Marshal(sandboxState)
	if err != nil {
		return meta
	}
	var stateMap map[string]any
	if err := json.Unmarshal(stateValue, &stateMap); err != nil {
		return meta
	}
	if meta == nil {
		return map[string]any{mcpSandboxStateMetaCapability: stateMap}
	}
	metaMap, ok := meta.(map[string]any)
	if !ok {
		return meta
	}
	out := cloneAnyMap(metaMap)
	if out == nil {
		out = map[string]any{}
	}
	out[mcpSandboxStateMetaCapability] = stateMap
	return out
}

func mcpToolCallMetaWithThreadID(meta any, threadID string) any {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return cloneJSONValue(meta)
	}
	if meta == nil {
		return map[string]any{mcpToolThreadIDMetaKey: threadID}
	}
	values, ok := meta.(map[string]any)
	if !ok {
		return cloneJSONValue(meta)
	}
	out := cloneAnyMap(values)
	if out == nil {
		out = map[string]any{}
	}
	out[mcpToolThreadIDMetaKey] = threadID
	return out
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

func (s *MCPService) listInventoryForConfig(name string, config *ServerConfig, threadID string) (*stdioInventory, error) {
	roots := s.rootsForThread(threadID)
	if config != nil && strings.TrimSpace(config.URL) != "" {
		if err := ValidateServerAuth(name, config); err != nil {
			return nil, err
		}
		return listMCPHTTPInventoryWithOptions(s.httpClientForServer(name, config), name, threadID, roots)
	}
	if config != nil && strings.TrimSpace(config.Command) != "" {
		return listMCPStdioInventoryWithOptions(s.stdioClientForServer(name, config), name, threadID, roots)
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
	sharedHTTPClient := s.sharedHTTPClient
	sharedHTTPClientKey := s.sharedHTTPClientKey
	key := mcpHTTPConnectionCacheKey(config, openAIForm, sharedHTTPClientKey)
	if cached := s.httpClients[name]; cached != nil && cached.key == key && cached.client != nil && !cached.client.isClosed() {
		s.mu.Unlock()
		return cached.client
	}
	old := s.deleteHTTPClientLocked(name)
	cloned := cloneServerConfig(config)
	client := newMCPHTTPClientWithShared(&cloned, openAIForm, sharedHTTPClient)
	s.httpClients[name] = &cachedMCPHTTPClient{key: key, client: client}
	s.mu.Unlock()
	closeHTTPClients([]*httpClient{old})
	return client
}

func mcpHTTPConnectionCacheKey(config *ServerConfig, openAIForm bool, sharedHTTPClientKey string) string {
	return mcpConnectionCacheKey(config, openAIForm) + "|httpClient=" + sharedHTTPClientKey
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
	if config == nil {
		return fmt.Sprintf("openaiForm=%t", openAIForm)
	}
	cloned := cloneServerConfig(config)
	cloned.Enabled = false
	cloned.DisabledReason = ""
	cloned.Required = false
	cloned.EnabledTools = nil
	cloned.DisabledTools = nil
	cloned.OmitToolsFrom = nil
	cloned.DefaultToolsApprovalMode = nil
	cloned.Tools = nil
	cloned.StartupTimeout = 0
	cloned.ToolTimeout = 0
	applyHTTPRequest := cloned.ApplyHTTPRequest != nil
	cloned.ApplyHTTPRequest = nil
	data, err := json.Marshal(cloned)
	if err != nil {
		return fmt.Sprintf("%#v|openaiForm=%t|requestAuth=%t|protocolMode=%d", cloned, openAIForm, applyHTTPRequest, config.ProtocolMode)
	}
	return fmt.Sprintf("%s|openaiForm=%t|requestAuth=%t|protocolMode=%d", data, openAIForm, applyHTTPRequest, config.ProtocolMode)
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
		return newMCPStdioClient(config)
	}
	s.mu.Lock()
	if s.stdioClients == nil {
		s.stdioClients = map[string]*cachedMCPStdioClient{}
	}
	openAIForm := s.openAIForm
	key := mcpConnectionCacheKey(config, openAIForm)
	if cached := s.stdioClients[name]; cached != nil && cached.key == key && cached.client != nil && !cached.client.isClosed() {
		s.mu.Unlock()
		return cached.client
	}
	old := s.deleteStdioClientLocked(name)
	client := newMCPStdioClientWithOpenAIForm(config, openAIForm)
	s.stdioClients[name] = &cachedMCPStdioClient{key: key, client: client}
	s.mu.Unlock()
	closeStdioClients([]*stdioClient{old})
	return client
}

func (s *MCPService) elicitationHandler() MCPElicitationHandler {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.elicitation
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
		store = NewOAuthStore(config.CodexHome)
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
	closeStdioClients(clients)
}

func (s *MCPService) stdioClientsForCloseLocked() []*stdioClient {
	if s == nil || len(s.stdioClients) == 0 {
		return nil
	}
	clients := make([]*stdioClient, 0, len(s.stdioClients))
	for _, cached := range s.stdioClients {
		if cached != nil && cached.client != nil {
			clients = append(clients, cached.client)
		}
	}
	return clients
}

func (s *MCPService) deleteStdioClientLocked(name string) *stdioClient {
	if s == nil || s.stdioClients == nil {
		return nil
	}
	cached := s.stdioClients[name]
	delete(s.stdioClients, name)
	if cached == nil {
		return nil
	}
	return cached.client
}

func closeStdioClients(clients []*stdioClient) {
	for _, client := range clients {
		if client != nil {
			_ = client.Close()
		}
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
	status.PluginID = cloneStringPtr(status.PluginID)
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
