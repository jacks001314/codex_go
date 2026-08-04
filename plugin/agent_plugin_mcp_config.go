package plugin

// Rust parity: codex-rs/codex-mcp/src/agent_plugin_config.rs (bd12b3a9ec).
// Translates an Agent Plugins v1 `mcp.json` file into Codex MCP server
// configuration: normalizes stdio and streamable-http transports, expands
// PLUGIN_ROOT/PLUGIN_DATA placeholders, enforces contained paths and secure
// endpoints, and filters client-owned HTTP headers.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// AgentPluginMCPSchemaURI is the published Agent Plugins v1 MCP schema.
const AgentPluginMCPSchemaURI = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

const (
	pluginRootVariable = "PLUGIN_ROOT"
	pluginDataVariable = "PLUGIN_DATA"
)

// clientOwnedHTTPHeaders are transport-owned headers that Agent Plugins may not
// forward through the streamable HTTP transport.
var clientOwnedHTTPHeaders = []string{
	"accept",
	"authorization",
	"connection",
	"content-encoding",
	"content-length",
	"content-type",
	"host",
	"last-event-id",
	"mcp-protocol-version",
	"mcp-session-id",
	"proxy-authorization",
	"te",
	"trailer",
	"transfer-encoding",
	"upgrade",
	"user-agent",
}

// AgentPluginMCPServerParseError records why an individual server in an Agent
// Plugins mcp.json could not be normalized. Valid sibling servers still parse.
type AgentPluginMCPServerParseError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// AgentPluginMCPConfigParseOutcome carries the normalized server map together
// with per-server parse errors, mirroring Rust's PluginMcpConfigParseOutcome.
type AgentPluginMCPConfigParseOutcome struct {
	Servers map[string]map[string]any
	Errors  []AgentPluginMCPServerParseError
}

// ErrAgentPluginMCPUnsupportedSchema is returned when the file does not use a
// supported Agent Plugins MCP schema.
var ErrAgentPluginMCPUnsupportedSchema = errors.New("unsupported Agent Plugins MCP schema")

// ParseAgentPluginMCPConfig parses an Agent Plugins `mcp.json` and translates
// it into Codex MCP server configuration. pluginDataRoot may be empty, in which
// case `${PLUGIN_DATA}` expansion uses pluginRoot.
func ParseAgentPluginMCPConfig(contents string, pluginRoot string, pluginDataRoot string) (*AgentPluginMCPConfigParseOutcome, error) {
	if strings.TrimSpace(pluginDataRoot) == "" {
		pluginDataRoot = pluginRoot
	}
	var file struct {
		Schema     string                     `json:"$schema"`
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := decodeStrictJSON([]byte(contents), &file); err != nil {
		return nil, err
	}
	if file.Schema != AgentPluginMCPSchemaURI {
		return nil, fmt.Errorf("%w: got %q", ErrAgentPluginMCPUnsupportedSchema, file.Schema)
	}
	outcome := &AgentPluginMCPConfigParseOutcome{
		Servers: make(map[string]map[string]any, len(file.MCPServers)),
	}
	names := make([]string, 0, len(file.MCPServers))
	for name := range file.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		config, err := normalizeAgentPluginMCPServer(file.MCPServers[name], pluginRoot, pluginDataRoot)
		if err != nil {
			outcome.Errors = append(outcome.Errors, AgentPluginMCPServerParseError{Name: name, Message: err.Error()})
			continue
		}
		outcome.Servers[name] = config
	}
	return outcome, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func normalizeAgentPluginMCPServer(raw json.RawMessage, pluginRoot string, pluginDataRoot string) (map[string]any, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("Agent Plugins MCP server must be an object: %w", err)
	}
	var server struct {
		Type    string            `json:"type"`
		Command *string           `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		CWD     *string           `json:"cwd"`
		URL     *string           `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := decodeStrictJSON(raw, &server); err != nil {
		return nil, errors.New(err.Error())
	}
	switch server.Type {
	case "stdio":
		if rawNullField(object, "cwd") {
			return nil, errors.New("Agent Plugins MCP `cwd` must use its declared type when present")
		}
		command := ""
		if server.Command != nil {
			command = *server.Command
		}
		args := server.Args
		if args == nil {
			args = []string{}
		}
		env := server.Env
		if env == nil {
			env = map[string]string{}
		}
		return normalizeAgentPluginStdioServer(command, args, env, server.CWD, pluginRoot, pluginDataRoot)
	case "streamable-http":
		if rawNullField(object, "headers") {
			return nil, errors.New("Agent Plugins MCP `headers` must use its declared type when present")
		}
		urlValue := ""
		if server.URL != nil {
			urlValue = *server.URL
		}
		return normalizeAgentPluginHTTPServer(urlValue, server.Headers)
	case "sse":
		return nil, errors.New("Agent Plugins legacy SSE transport is not supported by Codex")
	default:
		return nil, fmt.Errorf("unsupported Agent Plugins MCP transport type %q", server.Type)
	}
}

func rawNullField(object map[string]json.RawMessage, field string) bool {
	value, ok := object[field]
	return ok && bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func normalizeAgentPluginStdioServer(command string, args []string, env map[string]string, cwd *string, pluginRoot string, pluginDataRoot string) (map[string]any, error) {
	hasWindowsPathPrefix := false
	if runtime.GOOS == "windows" {
		hasWindowsPathPrefix = filepath.VolumeName(command) != ""
	}
	isBareCommand := command != "" && !strings.Contains(command, "/") && !strings.Contains(command, "\\") && !hasWindowsPathPrefix
	isPluginRelativeCommand := strings.HasPrefix(command, "./") && isPortablePathSuffix(strings.TrimPrefix(command, "./"))
	if !isBareCommand && !isPluginRelativeCommand {
		return nil, errors.New("Agent Plugins stdio command must be a bare executable name or a contained `./` path")
	}
	for _, reserved := range []string{pluginRootVariable, pluginDataVariable} {
		for name := range env {
			if environmentVariableNamesMatch(name, reserved) {
				return nil, fmt.Errorf("Agent Plugins stdio `env` cannot override reserved variable `%s`", reserved)
			}
		}
	}
	normalizedEnv := env
	if runtime.GOOS == "windows" {
		normalizedEnv = make(map[string]string, len(env))
		for name, value := range env {
			normalizedName := strings.ToUpper(name)
			if _, exists := normalizedEnv[normalizedName]; exists {
				return nil, fmt.Errorf("duplicate case-insensitive Agent Plugins environment variable `%s`", name)
			}
			normalizedEnv[normalizedName] = value
		}
	}
	rootPath, err := absolutePluginPath(pluginRoot)
	if err != nil {
		return nil, err
	}
	dataRootPath, err := absolutePluginPath(pluginDataRoot)
	if err != nil {
		return nil, err
	}
	root := hostPathString(rootPath)
	dataRoot := hostPathString(dataRootPath)
	if strings.HasPrefix(command, "./") {
		resolved, resolveErr := resolveContainedHostPath(command, rootPath, rootPath)
		if resolveErr != nil {
			return nil, resolveErr
		}
		command = hostPathString(resolved)
	}
	for i := range args {
		args[i] = expandAgentPluginPlaceholders(args[i], root, dataRoot)
	}
	for name, value := range normalizedEnv {
		normalizedEnv[name] = expandAgentPluginPlaceholders(value, root, dataRoot)
	}
	cwdValue := "${PLUGIN_ROOT}"
	if cwd != nil {
		cwdValue = *cwd
	}
	cwdRoot, ok := parseAgentPluginCWD(cwdValue)
	if !ok {
		return nil, errors.New("Agent Plugins stdio `cwd` must be a contained `./`, `${PLUGIN_ROOT}`, or `${PLUGIN_DATA}` path")
	}
	cwdExpanded := expandAgentPluginPlaceholders(cwdValue, root, dataRoot)
	cwdBase := rootPath
	if cwdRoot == agentPluginCwdData {
		cwdBase = dataRootPath
	}
	cwdPath, resolveErr := resolveContainedHostPath(cwdExpanded, cwdBase, cwdBase)
	if resolveErr != nil {
		return nil, resolveErr
	}
	normalizedEnv[pluginRootVariable] = root
	normalizedEnv[pluginDataVariable] = dataRoot
	return map[string]any{
		"command": command,
		"args":    args,
		"env":     normalizedEnv,
		"cwd":     hostPathString(cwdPath),
	}, nil
}

func normalizeAgentPluginHTTPServer(rawURL string, headers map[string]string) (map[string]any, error) {
	if err := validateAgentPluginURL(rawURL); err != nil {
		return nil, err
	}
	if headers != nil {
		if err := validateAgentPluginHeaders(headers); err != nil {
			return nil, err
		}
		for name := range headers {
			for _, owned := range clientOwnedHTTPHeaders {
				if strings.EqualFold(name, owned) {
					delete(headers, name)
					break
				}
			}
		}
	}
	out := map[string]any{"url": rawURL}
	if len(headers) > 0 {
		out["http_headers"] = headers
	}
	return out, nil
}

func validateAgentPluginURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("Agent Plugins HTTP server requires a non-empty `url`")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid Agent Plugins MCP URL `%s`: %w", rawURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("Agent Plugins MCP URL must be absolute HTTP or HTTPS")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("Agent Plugins MCP URL must not contain user information or a fragment")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return errors.New("non-loopback Agent Plugins MCP endpoints must use HTTPS")
	}
	return nil
}

func isLoopbackHost(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func validateAgentPluginHeaders(headers map[string]string) error {
	seen := map[string]struct{}{}
	for name, value := range headers {
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate case-insensitive Agent Plugins HTTP header `%s`", name)
		}
		seen[key] = struct{}{}
		if !isValidHTTPHeaderName(name) {
			return fmt.Errorf("invalid Agent Plugins HTTP header name `%s`", name)
		}
		for _, b := range []byte(value) {
			if (b < 32 && b != '\t') || b == 127 {
				return fmt.Errorf("invalid Agent Plugins HTTP header value for `%s`", name)
			}
		}
	}
	return nil
}

func isValidHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, b := range []byte(name) {
		if !isASCIIAlnum(b) && !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b)) {
			return false
		}
	}
	return true
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func environmentVariableNamesMatch(left string, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// agentPluginCwdRoot identifies which plugin directory a `cwd` resolves into.
type agentPluginCwdRoot string

const (
	agentPluginCwdPackage agentPluginCwdRoot = "package"
	agentPluginCwdData    agentPluginCwdRoot = "data"
)

func parseAgentPluginCWD(value string) (agentPluginCwdRoot, bool) {
	if value == "./" {
		return agentPluginCwdPackage, true
	}
	if relative := strings.TrimPrefix(value, "./"); relative != value && isPortablePathSuffix(relative) {
		return agentPluginCwdPackage, true
	}
	for _, candidate := range []struct {
		placeholder string
		root        agentPluginCwdRoot
	}{
		{"${PLUGIN_ROOT}", agentPluginCwdPackage},
		{"${PLUGIN_DATA}", agentPluginCwdData},
	} {
		if value == candidate.placeholder {
			return candidate.root, true
		}
		if relative := strings.TrimPrefix(value, candidate.placeholder+"/"); relative != value && (relative == "" || isPortablePathSuffix(relative)) {
			return candidate.root, true
		}
	}
	return "", false
}

func expandAgentPluginPlaceholders(value string, pluginRoot string, pluginData string) string {
	const rootPlaceholder = "${PLUGIN_ROOT}"
	const dataPlaceholder = "${PLUGIN_DATA}"
	var output strings.Builder
	remaining := value
	for {
		rootIndex := strings.Index(remaining, rootPlaceholder)
		dataIndex := strings.Index(remaining, dataPlaceholder)
		var index int
		var placeholder string
		var replacement string
		switch {
		case rootIndex >= 0 && (dataIndex < 0 || rootIndex <= dataIndex):
			index, placeholder, replacement = rootIndex, rootPlaceholder, pluginRoot
		case dataIndex >= 0:
			index, placeholder, replacement = dataIndex, dataPlaceholder, pluginData
		default:
			output.WriteString(remaining)
			return output.String()
		}
		output.WriteString(remaining[:index])
		output.WriteString(replacement)
		remaining = remaining[index+len(placeholder):]
	}
}

func absolutePluginPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("failed to resolve plugin path: path is empty")
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to resolve plugin path: %w", err)
		}
		path = filepath.Join(cwd, path)
	}
	return resolveExistingPathPrefix(path)
}

func resolveContainedHostPath(value string, root string, allowedRoot string) (string, error) {
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, value)
	}
	resolved, err := resolveExistingPathPrefix(path)
	if err != nil {
		return "", err
	}
	if !pathWithin(resolved, allowedRoot) {
		return "", fmt.Errorf("expanded path `%s` must remain within `%s`", value, allowedRoot)
	}
	return resolved, nil
}

func resolveExistingPathPrefix(path string) (string, error) {
	existing := path
	missing := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return lexicalNormalize(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to resolve path `%s`: %w", path, err)
		}
		if info, statErr := os.Lstat(existing); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("failed to resolve symlinked path `%s`", path)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("failed to resolve path `%s`: %w", path, err)
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}

func pathWithin(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func lexicalNormalize(path string) string {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	absolute := strings.HasPrefix(rest, string(filepath.Separator))
	parts := strings.Split(rest, string(filepath.Separator))
	out := []string{}
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 && out[len(out)-1] != ".." {
				out = out[:len(out)-1]
			} else if !absolute {
				out = append(out, part)
			}
		default:
			out = append(out, part)
		}
	}
	normalized := strings.Join(out, string(filepath.Separator))
	if absolute {
		normalized = string(filepath.Separator) + normalized
	}
	if normalized == "" {
		return volume
	}
	return volume + normalized
}

func isPortablePathSuffix(value string) bool {
	return value != "" && !strings.Contains(value, "\\")
}

func hostPathString(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	if strings.HasPrefix(path, `\\?\`) {
		rest := strings.TrimPrefix(path, `\\?\`)
		if strings.HasPrefix(rest, `UNC\`) {
			return `\\` + strings.TrimPrefix(rest, `UNC\`)
		}
		return rest
	}
	return path
}
