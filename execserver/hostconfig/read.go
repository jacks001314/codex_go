// Package hostconfig implements the executor-local configuration read
// (`environmentConfig/read`) for a Go host, mirroring Rust
// codex-rs/exec-server/src/environment_config.rs::read_environment_config.
//
// It lives outside the execserver package because Go's config package depends
// on the agent layer, so the protocol package cannot import configuration
// itself; a host (the exec-server command) registers this reader.
package hostconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex_go/config"
	"codex_go/execserver"
	"codex_go/utils"

	"github.com/pelletier/go-toml/v2"
)

// Reader serves exec-server environment config reads from the local host's
// configuration model.
type Reader struct{}

var _ execserver.EnvironmentConfigReader = (*Reader)(nil)

// NewReader returns the standard host reader.
func NewReader() *Reader {
	return &Reader{}
}

// ReadEnvironmentConfig mirrors Rust's read_environment_config: validate the
// selectors, load the executor-local layers, add the startup sandbox preference
// as a session-flags layer, then project both stacks to the requested paths.
func (r *Reader) ReadEnvironmentConfig(params *execserver.EnvironmentConfigReadParams, preferMXC *bool) (*execserver.EnvironmentConfigReadResponse, error) {
	if params == nil {
		params = &execserver.EnvironmentConfigReadParams{}
	}
	if err := ValidatePaths(params); err != nil {
		return nil, execserver.InvalidEnvironmentConfigReadParams(err.Error())
	}
	cwd, err := ReadCWD(params.CWD)
	if err != nil {
		return nil, execserver.InvalidEnvironmentConfigReadParams(err.Error())
	}
	codexHome, err := config.FindCodexHome()
	if err != nil {
		return nil, execserver.InternalEnvironmentConfigReadError(fmt.Sprintf("failed to find Codex home: %v", err))
	}
	layers, err := config.LoadLocalConfigLayers(codexHome, cwd)
	if err != nil {
		return nil, execserver.InternalEnvironmentConfigReadError(fmt.Sprintf("failed to load executor-local config: %v", err))
	}
	if preferMXC != nil {
		// Rust retains only the CLI sandbox preference from startup overrides and
		// inserts it as a session-flags layer above project config but below the
		// legacy managed file.
		document := config.BuildCLIOverridesLayer([]config.Override{{Path: "features.prefer_mxc", Value: *preferMXC}})
		layers.Config = layers.Config.InsertSessionFlagsLayer(document, cwd)
	}
	projected := layers.Project(params.ConfigPaths, params.RequirementsPaths)

	configStack, err := serializeLayerStack(projected.Config)
	if err != nil {
		return nil, err
	}
	requirementsStack, err := serializeLayerStack(projected.Requirements)
	if err != nil {
		return nil, err
	}
	codexHomeURI, err := utils.FromHostNativePath(codexHome)
	if err != nil {
		return nil, execserver.InternalEnvironmentConfigReadError(fmt.Sprintf("failed to encode Codex home %s: %v", codexHome, err))
	}

	return &execserver.EnvironmentConfigReadResponse{
		UserHomeDir:  homeURI(),
		CodexHomeDir: codexHomeURI.String(),
		Hostname:     hostname(),
		Config:       configStack,
		Requirements: requirementsStack,
	}, nil
}

// ValidatePaths mirrors Rust's validate_paths: at least one selector list must
// be present, and no selector may be empty because an empty path would select
// the whole document.
func ValidatePaths(params *execserver.EnvironmentConfigReadParams) error {
	if len(params.ConfigPaths) == 0 && len(params.RequirementsPaths) == 0 {
		return fmt.Errorf("at least one config or requirements path is required")
	}
	for _, path := range params.ConfigPaths {
		if len(path) == 0 {
			return fmt.Errorf("TOML paths must contain at least one key segment")
		}
	}
	for _, path := range params.RequirementsPaths {
		if len(path) == 0 {
			return fmt.Errorf("TOML paths must contain at least one key segment")
		}
	}
	return nil
}

// ReadCWD resolves the required absolute cwd selector.
func ReadCWD(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("cwd is required")
	}
	uri, err := utils.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("cwd must be an absolute file URI: %w", err)
	}
	native, err := uri.HostNativePath()
	if err != nil {
		return "", fmt.Errorf("cwd must be an absolute file URI: %w", err)
	}
	if !filepath.IsAbs(native) {
		return "", fmt.Errorf("cwd must be an absolute file URI: %s", raw)
	}
	return native, nil
}

func serializeLayerStack(stack config.LocalTomlLayerStack) (execserver.EnvironmentConfigLayerStack, error) {
	layers := make([]execserver.EnvironmentConfigLayer, 0, len(stack.Layers))
	for _, layer := range stack.Layers {
		baseDir, err := utils.FromHostNativePath(layer.BaseDir)
		if err != nil {
			return execserver.EnvironmentConfigLayerStack{}, execserver.InternalEnvironmentConfigReadError(
				fmt.Sprintf("failed to encode layer base directory %s: %v", layer.BaseDir, err))
		}
		encoded, err := toml.Marshal(layer.TOML)
		if err != nil {
			return execserver.EnvironmentConfigLayerStack{}, execserver.InternalEnvironmentConfigReadError(
				fmt.Sprintf("failed to serialize executor-local config: %v", err))
		}
		layers = append(layers, execserver.EnvironmentConfigLayer{
			Source:  layer.Source,
			BaseDir: baseDir.String(),
			TOML:    string(encoded),
		})
	}
	return execserver.EnvironmentConfigLayerStack{
		Layers:              layers,
		CloudInsertionIndex: stack.CloudInsertionIndex,
	}, nil
}

func homeURI() *string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	uri, err := utils.FromHostNativePath(home)
	if err != nil {
		return nil
	}
	value := uri.String()
	return &value
}

func hostname() *string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return nil
	}
	value := strings.TrimSpace(name)
	return &value
}
