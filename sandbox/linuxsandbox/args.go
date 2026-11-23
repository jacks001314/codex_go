package linuxsandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrInvalidCommandArgs = fmt.Errorf("invalid linux sandbox command args")

// LinuxSandboxPIDNamespace selects the helper's PID namespace policy.
//
// Inheritance is a trusted executor startup choice (Rust #47989): it is never
// derived from repository config, a command environment or a tool-call payload.
// It reuses the caller's PID namespace and `/proc` together, so sandboxed
// commands can signal other same-UID processes, including the executor.
type LinuxSandboxPIDNamespace string

const (
	// LinuxSandboxPIDNamespaceIsolate creates a PID namespace, keeping the
	// legacy `/proc` mount fallback.
	LinuxSandboxPIDNamespaceIsolate LinuxSandboxPIDNamespace = "isolate"
	// LinuxSandboxPIDNamespaceInherit reuses the caller's PID namespace and
	// `/proc` and omits `--unshare-pid` and `--as-pid-1`.
	LinuxSandboxPIDNamespaceInherit LinuxSandboxPIDNamespace = "inherit"
)

// ParseLinuxSandboxPIDNamespace applies the helper's value parser. An empty
// value keeps the isolate default, so callers that never set the startup flag
// behave exactly as before.
func ParseLinuxSandboxPIDNamespace(value string) (LinuxSandboxPIDNamespace, error) {
	switch strings.TrimSpace(value) {
	case "", string(LinuxSandboxPIDNamespaceIsolate):
		return LinuxSandboxPIDNamespaceIsolate, nil
	case string(LinuxSandboxPIDNamespaceInherit):
		return LinuxSandboxPIDNamespaceInherit, nil
	}
	return "", fmt.Errorf("%w: expected 'isolate' or 'inherit'", ErrInvalidCommandArgs)
}

// LinuxSandboxCommandArgsOptions is the complete helper argv surface.
type LinuxSandboxCommandArgsOptions struct {
	SandboxExe           string
	Command              []string
	CommandCWD           string
	ProfileJSON          string
	SandboxPolicyCWD     string
	UseLegacyLandlock    bool
	AllowNetworkForProxy bool
	ManagedNetworkJSON   string
	TransparentTCP       bool
	// PIDNamespace is empty for the isolate default.
	PIDNamespace LinuxSandboxPIDNamespace
}

func CreateCommandArgs(command []string, commandCWD string, profileJSON string, sandboxPolicyCWD string, useLegacyLandlock bool, allowNetworkForProxy bool) ([]string, error) {
	return CreateCommandArgsWithSandboxExe("", command, commandCWD, profileJSON, sandboxPolicyCWD, useLegacyLandlock, allowNetworkForProxy)
}

// CreateCommandArgsWithManagedNetwork forwards the effective managed-network
// context in addition to the legacy proxy flag, mirroring the Rust helper's
// `--managed-network <json>` argument.
func CreateCommandArgsWithManagedNetwork(command []string, commandCWD string, profileJSON string, sandboxPolicyCWD string, useLegacyLandlock bool, allowNetworkForProxy bool, managedNetworkJSON string) ([]string, error) {
	return CreateCommandArgsWithSandboxExeAndManagedNetwork("", command, commandCWD, profileJSON, sandboxPolicyCWD, useLegacyLandlock, allowNetworkForProxy, managedNetworkJSON)
}

func CreateCommandArgsWithSandboxExe(sandboxExe string, command []string, commandCWD string, profileJSON string, sandboxPolicyCWD string, useLegacyLandlock bool, allowNetworkForProxy bool) ([]string, error) {
	return CreateCommandArgsWithSandboxExeAndManagedNetwork(sandboxExe, command, commandCWD, profileJSON, sandboxPolicyCWD, useLegacyLandlock, allowNetworkForProxy, "")
}

// CreateCommandArgsWithSandboxExeAndManagedNetwork builds the sandbox helper
// argv for one command.
//
// A managed-network context is proxy-mode evidence on its own: the Rust helper
// sets `allow_network_for_proxy` from `managed_network.is_some()`, so a context
// implies `--allow-network-for-proxy` exactly like the upstream CLI shape. With
// an empty context the emitted argv is byte-identical to
// CreateCommandArgsWithSandboxExe.
func CreateCommandArgsWithSandboxExeAndManagedNetwork(sandboxExe string, command []string, commandCWD string, profileJSON string, sandboxPolicyCWD string, useLegacyLandlock bool, allowNetworkForProxy bool, managedNetworkJSON string) ([]string, error) {
	return CreateCommandArgsWithSandboxExeAndManagedNetworkAndTransparentTCP(sandboxExe, command, commandCWD, profileJSON, sandboxPolicyCWD, useLegacyLandlock, allowNetworkForProxy, managedNetworkJSON, false)
}

// CreateCommandArgsWithSandboxExeAndManagedNetworkAndTransparentTCP adds the
// opt-in transparent TCP switch. The Linux helper rejects it without managed
// networking, so the flag is only emitted with a managed-network launch
// (Rust #61372 SandboxManager::with_linux_sandbox_transparent_tcp).
func CreateCommandArgsWithSandboxExeAndManagedNetworkAndTransparentTCP(sandboxExe string, command []string, commandCWD string, profileJSON string, sandboxPolicyCWD string, useLegacyLandlock bool, allowNetworkForProxy bool, managedNetworkJSON string, transparentTCP bool) ([]string, error) {
	return CreateCommandArgsForLinuxSandbox(LinuxSandboxCommandArgsOptions{
		SandboxExe:           sandboxExe,
		Command:              command,
		CommandCWD:           commandCWD,
		ProfileJSON:          profileJSON,
		SandboxPolicyCWD:     sandboxPolicyCWD,
		UseLegacyLandlock:    useLegacyLandlock,
		AllowNetworkForProxy: allowNetworkForProxy,
		ManagedNetworkJSON:   managedNetworkJSON,
		TransparentTCP:       transparentTCP,
	})
}

// LinuxSandboxMode is the effective Linux sandbox backend for one launch
// (Rust #62215 LinuxSandboxMode). Bubblewrap is the default; the legacy
// Landlock path is a compatibility mode that cannot carry a managed proxy
// route.
type LinuxSandboxMode struct {
	// LegacyLandlock selects the retained Landlock seccomp backend.
	LegacyLandlock bool
	// ManagedNetwork reports that a managed proxy route is attached, which
	// requires bubblewrap's network namespace.
	ManagedNetwork bool
}

// ResolveLinuxSandboxMode mirrors Rust's LinuxSandboxMode::resolve: a managed
// proxy route takes precedence over a requested legacy Landlock mode, so the
// WSL1 check and the helper argv always agree on the effective backend.
func ResolveLinuxSandboxMode(useLegacyLandlock bool, managedNetwork bool) LinuxSandboxMode {
	return LinuxSandboxMode{
		LegacyLandlock: useLegacyLandlock && !managedNetwork,
		ManagedNetwork: managedNetwork,
	}
}

// CreateCommandArgsForLinuxSandbox builds the helper argv for one command.
//
// `--inherit-pid-namespace` is emitted first, matching Rust
// `SandboxManager::with_linux_sandbox_pid_namespace` (#47989): default
// invocations stay byte-identical so they remain compatible with older helpers.
func CreateCommandArgsForLinuxSandbox(opts LinuxSandboxCommandArgsOptions) ([]string, error) {
	sandboxExe := opts.SandboxExe
	command := opts.Command
	commandCWD := opts.CommandCWD
	profileJSON := opts.ProfileJSON
	sandboxPolicyCWD := opts.SandboxPolicyCWD
	useLegacyLandlock := opts.UseLegacyLandlock
	allowNetworkForProxy := opts.AllowNetworkForProxy
	managedNetworkJSON := opts.ManagedNetworkJSON
	transparentTCP := opts.TransparentTCP
	pidNamespace, err := ParseLinuxSandboxPIDNamespace(string(opts.PIDNamespace))
	if err != nil {
		return nil, err
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("%w: command is required", ErrInvalidCommandArgs)
	}
	if strings.TrimSpace(profileJSON) == "" {
		return nil, fmt.Errorf("%w: permission profile is required", ErrInvalidCommandArgs)
	}
	sandboxExe = strings.TrimSpace(sandboxExe)
	if sandboxExe == "" {
		sandboxExe = "codex-linux-sandbox"
	} else {
		sandboxExe = filepath.Clean(sandboxExe)
	}
	if strings.TrimSpace(commandCWD) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		commandCWD = cwd
	}
	if strings.TrimSpace(sandboxPolicyCWD) == "" {
		sandboxPolicyCWD = commandCWD
	}
	commandCWD = filepath.Clean(commandCWD)
	sandboxPolicyCWD = filepath.Clean(sandboxPolicyCWD)
	args := []string{
		sandboxExe,
	}
	if pidNamespace == LinuxSandboxPIDNamespaceInherit {
		args = append(args, "--inherit-pid-namespace")
	}
	args = append(args,
		"--sandbox-policy-cwd", sandboxPolicyCWD,
		"--command-cwd", commandCWD,
		"--permission-profile", profileJSON,
	)
	managedNetworkJSON = strings.TrimSpace(managedNetworkJSON)
	mode := ResolveLinuxSandboxMode(useLegacyLandlock, allowNetworkForProxy || managedNetworkJSON != "")
	if mode.LegacyLandlock {
		args = append(args, "--use-legacy-landlock")
	}
	if mode.ManagedNetwork {
		args = append(args, "--allow-network-for-proxy")
	}
	if managedNetworkJSON != "" {
		args = append(args, "--managed-network", managedNetworkJSON)
		// Rust #61851 SandboxManager: the prepared context can request capture
		// on its own, without the executor startup switch.
		var context struct {
			TransparentTCP bool `json:"transparentTcp"`
		}
		if err := json.Unmarshal([]byte(managedNetworkJSON), &context); err == nil && context.TransparentTCP {
			transparentTCP = true
		}
	}
	if transparentTCP && mode.ManagedNetwork {
		args = append(args, "--transparent-tcp")
	}
	args = append(args, "--")
	args = append(args, command...)
	return args, nil
}
