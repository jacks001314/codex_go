package appserver

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

const (
	defaultInitializeOriginator = "codex_cli_rs"
	originatorOverrideEnv       = "CODEX_INTERNAL_ORIGINATOR_OVERRIDE"
	defaultCodexVersion         = "0.0.0"
)

// buildVersion is injected alongside doctor.buildVersion by release builds.
// Keeping the app-server version on the same build metadata path is important:
// VS Code clients learn the server version from initialize.userAgent, not from
// the CLI --version output.
var buildVersion = defaultCodexVersion

type ClientInfo struct {
	Name    string  `json:"name"`
	Title   *string `json:"title,omitempty"`
	Version string  `json:"version"`
}

type InitializeCapabilities struct {
	ExperimentalAPI                bool     `json:"experimentalApi,omitempty"`
	RequestAttestation             bool     `json:"requestAttestation,omitempty"`
	OptOutNotificationMethods      []string `json:"optOutNotificationMethods,omitempty"`
	MCPServerOpenAIFormElicitation bool     `json:"mcpServerOpenaiFormElicitation,omitempty"`
	// MCPServerStandardFormInput mirrors the client-only
	// "openai/standard-form-input" extension: it enables surfacing standard MCP
	// forms in full-access user threads but is never advertised to MCP servers.
	MCPServerStandardFormInput bool `json:"mcpServerStandardFormInput,omitempty"`
}

type InitializeParams struct {
	ClientInfo   ClientInfo              `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities,omitempty"`
}

func (p *InitializeParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.ClientInfo.Name) == "" {
		return fmt.Errorf("%w: clientInfo.name is required", ErrInvalidRequest)
	}
	if !validHTTPHeaderValue(p.ClientInfo.Name) {
		return jsonRPCInvalidRequest(fmt.Sprintf("Invalid clientInfo.name: '%s'. Must be a valid HTTP header value.", p.ClientInfo.Name))
	}
	if strings.TrimSpace(p.ClientInfo.Version) == "" {
		return fmt.Errorf("%w: clientInfo.version is required", ErrInvalidRequest)
	}
	return nil
}

type InitializeResponse struct {
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
	UserAgent      string `json:"userAgent"`
}

func NewInitializeResponse(codexHome string, userAgent string) *InitializeResponse {
	return &InitializeResponse{
		CodexHome:      codexHome,
		PlatformFamily: PlatformFamily(runtime.GOOS),
		PlatformOS:     PlatformOS(runtime.GOOS),
		UserAgent:      userAgent,
	}
}

func ParseAppServerVersionFromUserAgent(userAgent string) (string, error) {
	_, rest, ok := strings.Cut(userAgent, "/")
	if !ok {
		return "", fmt.Errorf("%w: app-server user-agent omitted version separator", ErrInvalidRequest)
	}
	version := strings.Fields(rest)
	if len(version) == 0 || version[0] == "" {
		return "", fmt.Errorf("%w: app-server user-agent omitted version", ErrInvalidRequest)
	}
	return version[0], nil
}

func InitializeUserAgent(info ClientInfo) string {
	originator := initializeOriginator(info)
	version := appServerVersion()
	suffix := initializeUserAgentSuffix(info)
	candidate := fmt.Sprintf("%s/%s (%s; %s) go%s", originator, version, runtime.GOOS, runtime.GOARCH, suffix)
	return sanitizeHeaderValue(candidate, fmt.Sprintf("%s/%s", defaultInitializeOriginator, version))
}

func appServerVersion() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_GO_VERSION")); value != "" {
		return value
	}
	if value := strings.TrimSpace(buildVersion); value != "" && value != defaultCodexVersion {
		return value
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return defaultCodexVersion
}

func initializeUserAgentSuffix(info ClientInfo) string {
	name := strings.TrimSpace(info.Name)
	version := strings.TrimSpace(info.Version)
	switch name {
	case "codex_app_server_daemon", "codex-backend":
		return ""
	}
	if name == "" || version == "" || !validHTTPHeaderValue(name) || !validHTTPHeaderValue(version) {
		return ""
	}
	return fmt.Sprintf(" (%s; %s)", name, version)
}

func initializeOriginator(info ClientInfo) string {
	if override, ok := os.LookupEnv(originatorOverrideEnv); ok {
		if validHTTPHeaderValue(override) {
			return override
		}
		return defaultInitializeOriginator
	}
	name := info.Name
	switch name {
	case "codex_app_server_daemon", "codex-backend":
		return defaultInitializeOriginator
	default:
		if strings.TrimSpace(name) == "" || !validHTTPHeaderValue(name) {
			return defaultInitializeOriginator
		}
		return name
	}
}

func validHTTPHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == 0x7f || (b < 0x20 && b != '\t') {
			return false
		}
	}
	return true
}

func sanitizeHeaderValue(value string, fallback string) string {
	if validHTTPHeaderValue(value) {
		return value
	}
	var builder strings.Builder
	builder.Grow(len(value))
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == 0x7f || (b < 0x20 && b != '\t') {
			builder.WriteByte('_')
			continue
		}
		builder.WriteByte(b)
	}
	sanitized := builder.String()
	if validHTTPHeaderValue(sanitized) {
		return sanitized
	}
	return fallback
}

func PlatformFamily(goos string) string {
	switch goos {
	case "windows":
		return "windows"
	default:
		return "unix"
	}
}

func PlatformOS(goos string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	default:
		if strings.TrimSpace(goos) == "" {
			return runtime.GOOS
		}
		return goos
	}
}
