package app

import (
	"path/filepath"
	"strings"
	"time"

	"codex_go/internal/appserver"
	"codex_go/internal/mcp"
	"codex_go/internal/plugin"
)

// Rust parity subset: codex-rs/tui/src/app/background_requests.rs.

const (
	TokenActivityFetchTimeout     = 15 * time.Second
	RateLimitResetRequestTimeout  = 15 * time.Second
	WorkspaceHeadlineFetchTimeout = 2 * time.Second
)

var CLIHiddenPluginMarketplaces = []string{"openai-bundled"}

type BackgroundRequest struct {
	ID     string
	Active bool
}

type BackgroundRequestRegistry struct {
	active map[string]BackgroundRequest
}

func NewBackgroundRequestRegistry() *BackgroundRequestRegistry {
	return &BackgroundRequestRegistry{active: map[string]BackgroundRequest{}}
}

func (r *BackgroundRequestRegistry) Start(id string) bool {
	if r == nil || id == "" {
		return false
	}
	r.ensure()
	if r.active[id].Active {
		return false
	}
	r.active[id] = BackgroundRequest{ID: id, Active: true}
	return true
}

func (r *BackgroundRequestRegistry) Finish(id string) bool {
	if r == nil || id == "" {
		return false
	}
	r.ensure()
	if !r.active[id].Active {
		return false
	}
	delete(r.active, id)
	return true
}

func (r *BackgroundRequestRegistry) IsActive(id string) bool {
	if r == nil || id == "" {
		return false
	}
	r.ensure()
	return r.active[id].Active
}

func (r *BackgroundRequestRegistry) ActiveCount() int {
	if r == nil {
		return 0
	}
	r.ensure()
	return len(r.active)
}

func (r *BackgroundRequestRegistry) ensure() {
	if r.active == nil {
		r.active = map[string]BackgroundRequest{}
	}
}

func MCPInventoryRequestThreadID(activeThreadID string, requestedThreadID string, navigation *AgentNavigationState) (string, bool) {
	activeThreadID, activeOK := ParseAppServerThreadID(activeThreadID)
	requestedThreadID, requestedOK := ParseAppServerThreadID(requestedThreadID)
	if !activeOK || !requestedOK || requestedThreadID != activeThreadID {
		return "", false
	}
	if navigation != nil {
		if entry, ok := navigation.Get(requestedThreadID); ok && entry.IsClosed {
			return "", false
		}
	}
	return requestedThreadID, true
}

type PluginRemoteSectionError struct {
	SectionID string
	Label     string
	Message   string
}

func PluginRemoteSectionErrorMessage(label string, err string) string {
	nextStep := PluginRemoteSectionErrorNextStep(label, err)
	if nextStep == "" {
		return err
	}
	return err + " " + nextStep
}

func PluginRemoteSectionErrorNextStep(label string, err string) string {
	err = strings.ToLower(err)
	switch {
	case strings.Contains(err, "api key auth is not supported"):
		return "Sign in with ChatGPT auth; API key auth cannot load remote plugin catalogs."
	case strings.Contains(err, "authentication required") ||
		strings.Contains(err, "not signed in") ||
		strings.Contains(err, "not logged in"):
		return "Sign in to ChatGPT, then try loading this section again."
	case strings.Contains(err, "codex plugins are disabled") ||
		strings.Contains(err, "plugin sharing is disabled") ||
		strings.Contains(err, "plugin sharing is not enabled") ||
		strings.Contains(err, "feature disabled"):
		return "Ask a workspace admin to enable Codex plugins or plugin sharing."
	case strings.Contains(err, "workspace") && (strings.Contains(err, "access") || strings.Contains(err, "mismatch")):
		return "Switch to the matching workspace or ask the sharer for access."
	case strings.Contains(err, "not found") || strings.Contains(err, "status 404"):
		return "Check that you are signed in to the correct workspace and still have access."
	case strings.Contains(err, "old build") || strings.Contains(err, "update codex") || strings.Contains(err, "stale"):
		return "Update Codex, then try opening the shared plugin again."
	case strings.Contains(err, "service unavailable") ||
		strings.Contains(err, "temporarily unavailable") ||
		strings.Contains(err, "status 503") ||
		strings.Contains(err, "failed to send") ||
		strings.Contains(err, "request") ||
		strings.Contains(err, "status"):
		return "Try again later; local plugin functionality is still available."
	case strings.Contains(err, "disabled by admin") || strings.Contains(err, "admin disabled"):
		return "Ask a workspace admin to confirm plugin access."
	case label == "Shared with me" && strings.Contains(err, "plugin") && strings.Contains(err, "disabled"):
		return "Ask the sharer or a workspace admin to confirm plugin access."
	default:
		return ""
	}
}

func PluginSharingDisabledRemoteSectionError() PluginRemoteSectionError {
	return PluginRemoteSectionError{
		SectionID: "shared-with-me",
		Label:     "Shared with me",
		Message:   "Plugin sharing is disabled for this Codex session. Enable plugin sharing to load shared plugins.",
	}
}

func HideCLIOnlyPluginMarketplaces(response *plugin.PluginListResponse) {
	if response == nil || len(response.Marketplaces) == 0 {
		return
	}
	hidden := make(map[string]bool, len(CLIHiddenPluginMarketplaces))
	for _, name := range CLIHiddenPluginMarketplaces {
		hidden[name] = true
	}
	kept := response.Marketplaces[:0]
	for _, marketplace := range response.Marketplaces {
		if !hidden[marketplace.Name] {
			kept = append(kept, marketplace)
		}
	}
	response.Marketplaces = kept
}

func MarketplaceAddSourceForRequest(cwd string, source string) string {
	baseSource, suffix := splitMarketplaceSourceSuffix(source)
	if !isRelativeMarketplaceAddSource(baseSource) {
		return source
	}
	baseSource = filepath.FromSlash(strings.ReplaceAll(baseSource, `\`, `/`))
	resolved := filepath.Clean(filepath.Join(cwd, baseSource))
	if suffix != "" {
		resolved += suffix
	}
	return resolved
}

func BuildFeedbackUploadParams(originThreadID string, rolloutPath string, category string, reason *string, turnID string, includeLogs bool) appserver.FeedbackUploadParams {
	var threadID *string
	if originThreadID != "" {
		threadID = &originThreadID
	}
	var extraLogFiles []string
	if includeLogs && rolloutPath != "" {
		extraLogFiles = []string{rolloutPath}
	}
	var tags map[string]string
	if turnID != "" {
		tags = map[string]string{"turn_id": turnID}
	}
	return appserver.FeedbackUploadParams{
		Classification: FeedbackClassification(category),
		Reason:         cloneStringPointer(reason),
		ThreadID:       threadID,
		IncludeLogs:    includeLogs,
		ExtraLogFiles:  extraLogFiles,
		Tags:           tags,
	}
}

func FeedbackClassification(category string) string {
	switch category {
	case "bad_result", "good_result", "bug", "safety_check":
		return category
	default:
		return "other"
	}
}

type MCPInventoryMaps struct {
	Tools             map[string]mcp.MCPToolInfo
	Resources         map[string][]mcp.MCPResource
	ResourceTemplates map[string][]mcp.MCPResourceTemplate
	AuthStatuses      map[string]mcp.MCPAuthStatus
}

func MCPInventoryMapsFromStatuses(statuses []mcp.MCPServerStatus) MCPInventoryMaps {
	out := MCPInventoryMaps{
		Tools:             map[string]mcp.MCPToolInfo{},
		Resources:         map[string][]mcp.MCPResource{},
		ResourceTemplates: map[string][]mcp.MCPResourceTemplate{},
		AuthStatuses:      map[string]mcp.MCPAuthStatus{},
	}
	for _, status := range statuses {
		serverName := status.Name
		if serverName == "" {
			serverName = status.Server.Name
		}
		out.AuthStatuses[serverName] = status.AuthStatus
		out.Resources[serverName] = append([]mcp.MCPResource(nil), status.Resources...)
		out.ResourceTemplates[serverName] = append([]mcp.MCPResourceTemplate(nil), status.ResourceTemplates...)
		for _, tool := range status.Tools {
			out.Tools["mcp__"+serverName+"__"+tool.Name] = tool
		}
	}
	return out
}

func splitMarketplaceSourceSuffix(source string) (string, string) {
	if index := strings.LastIndex(source, "#"); index >= 0 {
		return source[:index], source[index:]
	}
	if index := strings.LastIndex(source, "@"); index >= 0 {
		return source[:index], source[index:]
	}
	return source, ""
}

func isRelativeMarketplaceAddSource(source string) bool {
	return source == "." ||
		source == ".." ||
		strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, ".\\") ||
		strings.HasPrefix(source, "..\\")
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
