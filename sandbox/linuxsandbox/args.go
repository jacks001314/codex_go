package linuxsandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrInvalidCommandArgs = fmt.Errorf("invalid linux sandbox command args")

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
		"--sandbox-policy-cwd", sandboxPolicyCWD,
		"--command-cwd", commandCWD,
		"--permission-profile", profileJSON,
	}
	managedNetworkJSON = strings.TrimSpace(managedNetworkJSON)
	managedNetwork := allowNetworkForProxy || managedNetworkJSON != ""
	if useLegacyLandlock && !managedNetwork {
		args = append(args, "--use-legacy-landlock")
	}
	if managedNetwork {
		args = append(args, "--allow-network-for-proxy")
	}
	if managedNetworkJSON != "" {
		args = append(args, "--managed-network", managedNetworkJSON)
	}
	args = append(args, "--")
	args = append(args, command...)
	return args, nil
}
