package config

import (
	"fmt"
	"strings"
)

// execTrustConflictMessage is Rust's verbatim clap-independent diagnostic for
// `--trust --ignore-user-config` (codex-rs/exec/src/lib.rs, #60041).
const execTrustConflictMessage = "--trust cannot be combined with --ignore-user-config: saved project trust decisions must be checked"

// ExecProjectTrustWarning is the stderr warning `codex exec` prints when the
// project trust decision disabled at least one project-local layer
// (codex-rs/exec/src/lib.rs, #60041). The text is Rust's verbatim wording.
const ExecProjectTrustWarning = "warning: Untrusted project-local configuration is disabled. Run `codex exec --trust` to allow project configuration and MCP servers for this and future chats in the project, including MCP commands outside the sandbox. Explicitly untrusted projects must have their trust setting updated first."

// ExecProjectTrustOptions carries the inputs `codex exec` reads when it resolves
// `--trust`: the trusted configuration layers (packaged defaults, user,
// managed), the CLI `-c` overrides, and the working directory.
type ExecProjectTrustOptions struct {
	CodexHome string
	CWD       string
	Trust     bool
	// IgnoreUserConfig mirrors --ignore-user-config. It only conflicts with
	// --trust; on its own it changes nothing here.
	IgnoreUserConfig bool
	// Ephemeral mirrors --ephemeral and is deliberately never consulted: Rust
	// writes the grant to the user config file, so the decision outlives an
	// ephemeral run that keeps no session files. The field is recorded so the
	// persistence rule can be asserted instead of implied.
	Ephemeral bool
	// RawOverrides mirrors the merged root/exec `-c` values. Their `projects`
	// entries are dropped before the saved-decision lookup, matching Rust: a
	// command-line override must not be able to grant itself trust.
	RawOverrides         []string
	PackagedDefaultsPath string
	IncludeManagedConfig bool
	ManagedConfigPath    string
}

// ExecProjectTrustResult reports what `--trust` did.
type ExecProjectTrustResult struct {
	// Decision is the effective saved decision that governed the directory.
	Decision ProjectTrustDecision
	// Granted is true when this run wrote a trust grant.
	Granted bool
	// ConfigPath is the user config file the grant was (or would be) written to.
	ConfigPath string
}

// ResolveExecProjectTrust implements Rust's `codex exec --trust` gate
// (codex-rs/exec/src/lib.rs, #60041):
//
//   - `--trust` with `--ignore-user-config` is rejected before any write, since
//     the saved decisions live in the file that flag excludes;
//   - an explicitly untrusted project is refused and left untouched;
//   - an already trusted project needs no write;
//   - an undecided project gets `trust_level = "trusted"` saved under the key
//     project discovery selected (repository root, else project root).
//
// Without `--trust` nothing is read, written, or rejected.
func ResolveExecProjectTrust(opts ExecProjectTrustOptions) (ExecProjectTrustResult, error) {
	result := ExecProjectTrustResult{}
	if !opts.Trust {
		return result, nil
	}
	if opts.IgnoreUserConfig {
		return result, &ExecProjectTrustError{Message: execTrustConflictMessage}
	}
	values, err := execTrustDiscoveryValues(opts)
	if err != nil {
		return result, err
	}
	decision := ProjectTrustDecisionForDir(values, opts.CWD)
	result.Decision = decision
	if decision.Untrusted() {
		return result, &ExecProjectTrustError{
			Message: fmt.Sprintf("%s is marked as untrusted. Update its trust setting first.", decision.TrustKey),
		}
	}
	configPath := ConfigPath(opts.CodexHome)
	result.ConfigPath = configPath
	if decision.Trusted() {
		return result, nil
	}
	if strings.TrimSpace(decision.TrustKey) == "" {
		return result, &ExecProjectTrustError{Message: "project trust key is required"}
	}
	if err := SetProjectTrustLevel(configPath, decision.TrustKey, ProjectTrustLevelTrusted); err != nil {
		return result, err
	}
	result.Granted = true
	return result, nil
}

// ExecProjectTrustError reports a `--trust` refusal. The message text is Rust's
// verbatim wording so the CLI only has to print it.
type ExecProjectTrustError struct {
	Message string
}

func (e *ExecProjectTrustError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// execTrustDiscoveryValues builds the configuration snapshot Rust's
// load_bootstrap_config_or_exit supplies when `--trust` checks saved decisions:
// the trusted layers (packaged defaults, user config, managed config) with the
// CLI overrides applied, minus every `projects` entry.
func execTrustDiscoveryValues(opts ExecProjectTrustOptions) (map[string]any, error) {
	values := map[string]any{}
	if packagedDefaultsPath := strings.TrimSpace(opts.PackagedDefaultsPath); packagedDefaultsPath != "" {
		loaded, exists, err := loadConfigFileIfExists(packagedDefaultsPath)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("packaged defaults config file %s not found", packagedDefaultsPath)
		}
		values = loaded
	} else {
		// Rust always installs the embedded defaults as the lowest-precedence
		// layer when no packaged-defaults path is supplied (#38179).
		embedded, err := embeddedDefaultsValues()
		if err != nil {
			return nil, err
		}
		values = embedded
	}
	userValues, err := loadConfigFile(ConfigPath(opts.CodexHome))
	if err != nil {
		return nil, err
	}
	mergeConfigMaps(values, userValues)
	if opts.IncludeManagedConfig && !shouldIgnoreDefaultManagedConfig(opts.ManagedConfigPath) {
		managedValues, exists, err := loadConfigFileIfExists(managedConfigPath(opts.CodexHome, opts.ManagedConfigPath))
		if err != nil {
			return nil, err
		}
		if exists {
			mergeConfigMaps(values, managedValues)
		}
	}
	if len(opts.RawOverrides) > 0 {
		overrides, err := ParseOverrides(opts.RawOverrides)
		if err != nil {
			return nil, err
		}
		filtered := make([]Override, 0, len(overrides))
		for _, override := range overrides {
			if override.Path == "projects" || strings.HasPrefix(override.Path, "projects.") {
				continue
			}
			filtered = append(filtered, override)
		}
		if len(filtered) > 0 {
			mergeConfigMaps(values, BuildCLIOverridesLayer(filtered))
		}
	}
	return values, nil
}
