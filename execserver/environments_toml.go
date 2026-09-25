package execserver

// Executor environment configuration.
//
// Rust parity: codex-exec-server's environment_toml.rs and
// environment_provider.rs. `environments.toml` next to CODEX_HOME lists the
// executor environments this process can serve — one entry per environment,
// either a remote WebSocket endpoint (optionally authenticated with a bearer
// token) or a program spoken to over stdio — plus the default environment and
// whether the process's own local environment is included.

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	// EnvironmentsTOMLFile is the configuration file name inside CODEX_HOME.
	EnvironmentsTOMLFile = "environments.toml"
	// LocalEnvironmentID is this process's own environment.
	LocalEnvironmentID = "local"
	// RemoteEnvironmentID is the single environment selected by
	// CODEX_EXEC_SERVER_URL.
	RemoteEnvironmentID = "remote"
	// CodexExecServerURLEnvVarName mirrors Rust's CODEX_EXEC_SERVER_URL_ENV_VAR:
	// the environment variable that points Codex at a remote exec-server.
	CodexExecServerURLEnvVarName = "CODEX_EXEC_SERVER_URL"

	maxEnvironmentIDLen = 64
	// DefaultRemoteExecServerConnectTimeout and
	// DefaultRemoteExecServerInitializeTimeout mirror Rust's
	// DEFAULT_REMOTE_EXEC_SERVER_CONNECT_TIMEOUT / _INITIALIZE_TIMEOUT.
	DefaultRemoteExecServerConnectTimeout    = 10 * time.Second
	DefaultRemoteExecServerInitializeTimeout = 10 * time.Second
)

// ProtocolError mirrors Rust's ExecServerError::Protocol rendering.
type ProtocolError struct {
	Message string
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	return "exec-server protocol error: " + e.Message
}

func protocolErrorf(format string, args ...any) error {
	return &ProtocolError{Message: fmt.Sprintf(format, args...)}
}

// EnvironmentTransportKind distinguishes a remote WebSocket environment from a
// stdio program environment.
type EnvironmentTransportKind string

const (
	EnvironmentTransportWebSocket EnvironmentTransportKind = "websocket"
	EnvironmentTransportStdio     EnvironmentTransportKind = "stdio"
)

// StdioExecServerCommand is the program an stdio environment speaks JSON-RPC to.
type StdioExecServerCommand struct {
	Program string
	Args    []string
	Env     map[string]string
	CWD     string
}

// EnvironmentTransport describes how to reach one configured environment.
type EnvironmentTransport struct {
	Kind              EnvironmentTransportKind
	WebSocketURL      string
	HTTPHeaders       http.Header
	ConnectTimeout    time.Duration
	InitializeTimeout time.Duration
	Command           *StdioExecServerCommand
}

// NamedEnvironment pairs an environment id with its transport.
type NamedEnvironment struct {
	ID        string
	Transport EnvironmentTransport
}

// EnvironmentDefaultKind is how the default environment is selected.
type EnvironmentDefaultKind string

const (
	EnvironmentDefaultDisabled EnvironmentDefaultKind = "disabled"
	EnvironmentDefaultID       EnvironmentDefaultKind = "id"
)

// EnvironmentDefault mirrors Rust's EnvironmentDefault.
type EnvironmentDefault struct {
	Kind EnvironmentDefaultKind
	ID   string
}

// EnvironmentProviderSnapshot is the environment list a provider offers.
type EnvironmentProviderSnapshot struct {
	Environments []NamedEnvironment
	Default      EnvironmentDefault
	IncludeLocal bool
}

// EnvironmentID returns the default environment id when one is selected.
func (s EnvironmentProviderSnapshot) EnvironmentID() (string, bool) {
	if s.Default.Kind != EnvironmentDefaultID {
		return "", false
	}
	return s.Default.ID, true
}

// EnvironmentsTOML is the parsed `environments.toml` document.
type EnvironmentsTOML struct {
	Default      *string
	IncludeLocal *bool
	Environments []EnvironmentTOML
}

// EnvironmentTOML is one `[[environments]]` entry.
type EnvironmentTOML struct {
	ID         string
	URL        *string
	Token      *string
	Program    *string
	Args       *[]string
	Env        *map[string]string
	CWD        *string
	Connect    *float64
	Initialize *float64
}

type environmentsTOMLDoc struct {
	Default      *string              `toml:"default"`
	IncludeLocal *bool                `toml:"include_local"`
	Environments []environmentTOMLDoc `toml:"environments"`
}

type environmentTOMLDoc struct {
	ID         *string            `toml:"id"`
	URL        *string            `toml:"url"`
	Token      *string            `toml:"auth_bearer_token"`
	Program    *string            `toml:"program"`
	Args       *[]string          `toml:"args"`
	Env        *map[string]string `toml:"env"`
	CWD        *string            `toml:"cwd"`
	Connect    *float64           `toml:"connect_timeout_sec"`
	Initialize *float64           `toml:"initialize_timeout_sec"`
}

// ParseEnvironmentsTOML decodes `environments.toml`, rejecting unknown fields
// (Rust `deny_unknown_fields`) and never echoing configuration values in the
// failure text.
func ParseEnvironmentsTOML(contents []byte, path string) (*EnvironmentsTOML, error) {
	var doc environmentsTOMLDoc
	decoder := toml.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return nil, environmentTOMLDecodeError(err, path)
	}
	parsed := &EnvironmentsTOML{Default: doc.Default, IncludeLocal: doc.IncludeLocal}
	for _, entry := range doc.Environments {
		if entry.ID == nil {
			// Rust's EnvironmentToml requires `id`, so a missing value is a
			// parse failure rather than a validation failure.
			return nil, protocolErrorf("failed to parse environment config `%s`: missing field `id`", path)
		}
		parsed.Environments = append(parsed.Environments, EnvironmentTOML{
			ID:         *entry.ID,
			URL:        entry.URL,
			Token:      entry.Token,
			Program:    entry.Program,
			Args:       entry.Args,
			Env:        entry.Env,
			CWD:        entry.CWD,
			Connect:    entry.Connect,
			Initialize: entry.Initialize,
		})
	}
	return parsed, nil
}

// environmentTOMLDecodeError renders a decode failure without the offending
// values (the pelletier contextual text embeds the document).
func environmentTOMLDecodeError(err error, path string) error {
	var missing *toml.StrictMissingError
	if errors.As(err, &missing) && len(missing.Errors) > 0 {
		field := lastTOMLKeySegment(missing.Errors[0].Key())
		return protocolErrorf("failed to parse environment config `%s`: unknown field `%s`", path, field)
	}
	var decodeErr *toml.DecodeError
	if errors.As(err, &decodeErr) {
		line, column := decodeErr.Position()
		return protocolErrorf("failed to parse environment config `%s`: invalid TOML at line %d, column %d: %s", path, line, column, decodeErr.Error())
	}
	return protocolErrorf("failed to parse environment config `%s`: invalid TOML", path)
}

// lastTOMLKeySegment returns the field name of a strict-decode failure.
func lastTOMLKeySegment(key toml.Key) string {
	if len(key) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", key[len(key)-1])
}

// LoadEnvironmentsTOML reads the configuration file, returning (nil, nil) when
// it does not exist (Rust load_environments_toml).
func LoadEnvironmentsTOML(path string) (*EnvironmentsTOML, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, protocolErrorf("failed to read environment config `%s`: %v", path, err)
	}
	return ParseEnvironmentsTOML(contents, path)
}

// EnvironmentProviderFromCodexHome builds the provider snapshot for a Codex
// home: `environments.toml` when present, otherwise the CODEX_EXEC_SERVER_URL
// fallback (Rust environment_provider_from_codex_home).
func EnvironmentProviderFromCodexHome(codexHome string) (EnvironmentProviderSnapshot, error) {
	home := strings.TrimSpace(codexHome)
	config, err := LoadEnvironmentsTOML(filepath.Join(home, EnvironmentsTOMLFile))
	if err != nil {
		return EnvironmentProviderSnapshot{}, err
	}
	if config == nil {
		return DefaultEnvironmentProviderSnapshot(os.Getenv(CodexExecServerURLEnvVarName)), nil
	}
	return NewEnvironmentProviderSnapshot(config, home)
}

// NewEnvironmentProviderSnapshot validates a parsed configuration and builds the
// environment list, default selection and local-inclusion flag.
func NewEnvironmentProviderSnapshot(config *EnvironmentsTOML, configDir string) (EnvironmentProviderSnapshot, error) {
	if config == nil {
		config = &EnvironmentsTOML{}
	}
	includeLocal := true
	if config.IncludeLocal != nil {
		includeLocal = *config.IncludeLocal
	}
	ids := map[string]bool{}
	if includeLocal {
		ids[LocalEnvironmentID] = true
	}
	environments := make([]NamedEnvironment, 0, len(config.Environments))
	for _, entry := range config.Environments {
		id, transport, err := parseEnvironmentTOML(entry, configDir)
		if err != nil {
			return EnvironmentProviderSnapshot{}, err
		}
		if ids[id] {
			return EnvironmentProviderSnapshot{}, protocolErrorf("environment id `%s` is duplicated", id)
		}
		ids[id] = true
		environments = append(environments, NamedEnvironment{ID: id, Transport: transport})
	}
	defaultSelection, err := normalizeDefaultEnvironment(config.Default, includeLocal, ids)
	if err != nil {
		return EnvironmentProviderSnapshot{}, err
	}
	return EnvironmentProviderSnapshot{
		Environments: environments,
		Default:      defaultSelection,
		IncludeLocal: includeLocal,
	}, nil
}

func parseEnvironmentTOML(entry EnvironmentTOML, configDir string) (string, EnvironmentTransport, error) {
	id := entry.ID
	if err := validateEnvironmentID(id); err != nil {
		return "", EnvironmentTransport{}, err
	}
	hasArgs := entry.Args != nil
	hasEnv := entry.Env != nil
	hasCWD := entry.CWD != nil
	if entry.Program == nil && (hasArgs || hasEnv || hasCWD) {
		return "", EnvironmentTransport{}, protocolErrorf("environment `%s` args, env, and cwd require program", id)
	}
	if entry.URL == nil && entry.Token != nil {
		return "", EnvironmentTransport{}, protocolErrorf("environment `%s` auth_bearer_token requires url", id)
	}
	if entry.URL == nil && entry.Connect != nil {
		return "", EnvironmentTransport{}, protocolErrorf("environment `%s` connect_timeout_sec requires url", id)
	}
	connectTimeout := DefaultRemoteExecServerConnectTimeout
	if entry.Connect != nil {
		duration, err := secondsToDuration(*entry.Connect)
		if err != nil {
			return "", EnvironmentTransport{}, err
		}
		connectTimeout = duration
	}
	initializeTimeout := DefaultRemoteExecServerInitializeTimeout
	if entry.Initialize != nil {
		duration, err := secondsToDuration(*entry.Initialize)
		if err != nil {
			return "", EnvironmentTransport{}, err
		}
		initializeTimeout = duration
	}

	switch {
	case entry.URL != nil && entry.Program == nil:
		websocketURL, err := validateEnvironmentWebSocketURL(*entry.URL)
		if err != nil {
			return "", EnvironmentTransport{}, err
		}
		return id, EnvironmentTransport{
			Kind:              EnvironmentTransportWebSocket,
			WebSocketURL:      websocketURL,
			HTTPHeaders:       environmentAuthHeaders(entry.Token),
			ConnectTimeout:    connectTimeout,
			InitializeTimeout: initializeTimeout,
		}, nil
	case entry.URL == nil && entry.Program != nil:
		program := strings.TrimSpace(*entry.Program)
		if program == "" {
			return "", EnvironmentTransport{}, protocolErrorf("environment `%s` program cannot be empty", id)
		}
		cwd, err := normalizeStdioCWD(id, entry.CWD, configDir)
		if err != nil {
			return "", EnvironmentTransport{}, err
		}
		env := map[string]string{}
		if entry.Env != nil {
			for key, value := range *entry.Env {
				env[key] = value
			}
		}
		args := []string{}
		if entry.Args != nil {
			args = append(args, (*entry.Args)...)
		}
		return id, EnvironmentTransport{
			Kind:              EnvironmentTransportStdio,
			InitializeTimeout: initializeTimeout,
			Command: &StdioExecServerCommand{
				Program: program,
				Args:    args,
				Env:     env,
				CWD:     cwd,
			},
		}, nil
	default:
		return "", EnvironmentTransport{}, protocolErrorf("environment `%s` must set exactly one of url or program", id)
	}
}

func environmentAuthHeaders(token *string) http.Header {
	if token == nil {
		return nil
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+strings.TrimSpace(*token))
	return headers
}

// secondsToDuration mirrors Rust's Duration::try_from_secs_f64: a negative or
// non-finite value is rejected.
func secondsToDuration(seconds float64) (time.Duration, error) {
	if seconds < 0 || seconds != seconds || seconds > 1e15 {
		return 0, protocolErrorf("environment timeout seconds must be a non-negative number")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func normalizeStdioCWD(id string, cwd *string, configDir string) (string, error) {
	if cwd == nil {
		return "", nil
	}
	value := strings.TrimSpace(*cwd)
	if value == "" {
		return "", nil
	}
	if filepath.IsAbs(value) {
		return value, nil
	}
	if strings.TrimSpace(configDir) == "" {
		return "", protocolErrorf("environment `%s` cwd must be absolute", id)
	}
	return filepath.Join(configDir, value), nil
}

func normalizeDefaultEnvironment(defaultID *string, includeLocal bool, ids map[string]bool) (EnvironmentDefault, error) {
	if defaultID == nil {
		if includeLocal {
			return EnvironmentDefault{Kind: EnvironmentDefaultID, ID: LocalEnvironmentID}, nil
		}
		return EnvironmentDefault{Kind: EnvironmentDefaultDisabled}, nil
	}
	value := strings.TrimSpace(*defaultID)
	if value == "" {
		return EnvironmentDefault{}, protocolErrorf("default environment id cannot be empty")
	}
	if strings.EqualFold(value, "none") {
		return EnvironmentDefault{Kind: EnvironmentDefaultDisabled}, nil
	}
	if !ids[value] {
		return EnvironmentDefault{}, protocolErrorf("default environment `%s` is not configured", value)
	}
	return EnvironmentDefault{Kind: EnvironmentDefaultID, ID: value}, nil
}

func validateEnvironmentID(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return protocolErrorf("environment id cannot be empty")
	}
	if trimmed != id {
		return protocolErrorf("environment id `%s` must not contain surrounding whitespace", id)
	}
	if id == LocalEnvironmentID || strings.EqualFold(id, "none") {
		return protocolErrorf("environment id `%s` is reserved", id)
	}
	if len(id) > maxEnvironmentIDLen {
		return protocolErrorf("environment id `%s` cannot be longer than %d characters", id, maxEnvironmentIDLen)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return protocolErrorf("environment id `%s` must contain only ASCII letters, numbers, '-' or '_'", id)
		}
	}
	return nil
}

func validateEnvironmentWebSocketURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", protocolErrorf("environment url cannot be empty")
	}
	if !strings.HasPrefix(value, "ws://") && !strings.HasPrefix(value, "wss://") {
		return "", protocolErrorf("environment url `%s` must use ws:// or wss://", value)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", protocolErrorf("environment url `%s` is invalid", value)
	}
	return value, nil
}

// DefaultEnvironmentProviderSnapshot mirrors Rust's DefaultEnvironmentProvider:
// CODEX_EXEC_SERVER_URL selects a single `remote` environment (or disables the
// provider), otherwise the process serves its own local environment.
func DefaultEnvironmentProviderSnapshot(execServerURL string) EnvironmentProviderSnapshot {
	normalized, disabled := normalizeExecServerURL(execServerURL)
	snapshot := EnvironmentProviderSnapshot{}
	if normalized != "" {
		snapshot.Environments = append(snapshot.Environments, NamedEnvironment{
			ID: RemoteEnvironmentID,
			Transport: EnvironmentTransport{
				Kind:           EnvironmentTransportWebSocket,
				WebSocketURL:   normalized,
				ConnectTimeout: DefaultRemoteExecServerConnectTimeout,
			},
		})
	}
	hasRemote := len(snapshot.Environments) > 0
	snapshot.IncludeLocal = !disabled && !hasRemote
	switch {
	case disabled:
		snapshot.Default = EnvironmentDefault{Kind: EnvironmentDefaultDisabled}
	case hasRemote:
		snapshot.Default = EnvironmentDefault{Kind: EnvironmentDefaultID, ID: RemoteEnvironmentID}
	default:
		snapshot.Default = EnvironmentDefault{Kind: EnvironmentDefaultID, ID: LocalEnvironmentID}
	}
	return snapshot
}

// normalizeExecServerURL mirrors Rust's normalize_exec_server_url: a blank or
// `none` value disables the remote environment.
func normalizeExecServerURL(execServerURL string) (string, bool) {
	value := strings.TrimSpace(execServerURL)
	if value == "" {
		return "", false
	}
	if strings.EqualFold(value, "none") {
		return "", true
	}
	return value, false
}
