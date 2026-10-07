package execserver

import "errors"

// Rust parity: codex-rs/exec-server/src/environment_config.rs::
// read_environment_config and
// codex-rs/exec-server/src/server/handler.rs::environment_config_read.
//
// The executor reads its own local configuration and returns the selected TOML
// layers (with the base directory each layer's relative paths resolve against),
// so a client can resolve executor-local sandbox and MCP settings instead of
// assuming the client's own configuration applies to the executor's host.
//
// The protocol shapes live here; the host configuration model does not, because
// Go's config package depends on the agent layer (config/agent_roles.go), so
// execserver cannot import it. A host registers an EnvironmentConfigReader
// (`codex_go/execserver/hostconfig` is the standard one) and the advertised
// capability follows that registration, exactly as Rust's local executor
// advertises `environment_config_read` because it wires the handler.

// EnvironmentConfigReadParams is the `environmentConfig/read` request payload.
type EnvironmentConfigReadParams struct {
	CWD               string     `json:"cwd"`
	ConfigPaths       [][]string `json:"configPaths"`
	RequirementsPaths [][]string `json:"requirementsPaths"`
}

// EnvironmentConfigLayer is one selected executor-local TOML layer.
type EnvironmentConfigLayer struct {
	// Source is opaque provenance for diagnostics.
	Source string `json:"source"`
	// BaseDir is the directory used to interpret relative paths in TOML.
	BaseDir string `json:"baseDir"`
	// TOML is the selected raw TOML; path-bearing values are not normalized.
	TOML string `json:"toml"`
}

// EnvironmentConfigLayerStack is one ordered set of selected TOML layers.
type EnvironmentConfigLayerStack struct {
	// Layers are ordered from lowest to highest precedence.
	Layers []EnvironmentConfigLayer `json:"layers"`
	// CloudInsertionIndex is the position at which cloud-provided layers belong.
	CloudInsertionIndex int `json:"cloudInsertionIndex"`
}

// EnvironmentConfigReadResponse is the `environmentConfig/read` result.
type EnvironmentConfigReadResponse struct {
	// UserHomeDir is the executor user home used to expand `~`.
	UserHomeDir *string `json:"userHomeDir"`
	// CodexHomeDir is the executor Codex home used as the base directory for
	// cloud-provided layers.
	CodexHomeDir string `json:"codexHomeDir"`
	// Hostname is the executor hostname used to select matching remote sandbox
	// requirements.
	Hostname     *string                     `json:"hostname"`
	Config       EnvironmentConfigLayerStack `json:"config"`
	Requirements EnvironmentConfigLayerStack `json:"requirements"`
}

// EnvironmentConfigReader serves `environmentConfig/read` for one executor.
type EnvironmentConfigReader interface {
	ReadEnvironmentConfig(params *EnvironmentConfigReadParams, preferMXC *bool) (*EnvironmentConfigReadResponse, error)
}

// SetEnvironmentConfigReader installs the reader advertised by
// `environment/info`. Callers must install one before serving requests (the
// exec-server command does; a bare stub deliberately advertises false).
func (s *Server) SetEnvironmentConfigReader(reader EnvironmentConfigReader) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.environmentConfigReader = reader
	s.mu.Unlock()
}

func (s *Server) environmentConfigReaderForRead() EnvironmentConfigReader {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.environmentConfigReader
}

func (s *Server) environmentConfigReadSupported() bool {
	return s.environmentConfigReaderForRead() != nil
}

// environmentConfigRead serves `environmentConfig/read` for this connection.
func (s *Server) environmentConfigRead(params *EnvironmentConfigReadParams) (*EnvironmentConfigReadResponse, error) {
	reader := s.environmentConfigReaderForRead()
	if reader == nil {
		// The capability bit is false in this case, so a conforming client never
		// reaches this branch; report it as unavailable rather than as an
		// unknown method so the peer can tell the difference.
		return nil, requestError(-32603, "executor-local config read is not available")
	}
	return reader.ReadEnvironmentConfig(params, s.preferMXCValue())
}

// EnvironmentConfigReadError mirrors Rust ReadEnvironmentConfigError: selector
// problems are invalid params, everything else is an internal failure.
type EnvironmentConfigReadError struct {
	// InvalidParams selects the JSON-RPC invalid-params error.
	InvalidParams bool
	Message       string
}

func (e *EnvironmentConfigReadError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// InvalidEnvironmentConfigReadParams reports a rejected selector.
func InvalidEnvironmentConfigReadParams(message string) error {
	return &EnvironmentConfigReadError{InvalidParams: true, Message: message}
}

// InternalEnvironmentConfigReadError reports an executor-side failure.
func InternalEnvironmentConfigReadError(message string) error {
	return &EnvironmentConfigReadError{Message: message}
}

// environmentConfigReadFailure maps a reader error onto the JSON-RPC error the
// peer sees (Rust server/handler.rs::environment_config_read).
func environmentConfigReadFailure(err error) error {
	if err == nil {
		return nil
	}
	var readErr *EnvironmentConfigReadError
	if errors.As(err, &readErr) {
		if readErr.InvalidParams {
			return requestError(-32602, readErr.Message)
		}
		return requestError(-32603, readErr.Message)
	}
	return requestError(-32603, err.Error())
}
