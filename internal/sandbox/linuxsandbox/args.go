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

func CreateCommandArgsWithSandboxExe(sandboxExe string, command []string, commandCWD string, profileJSON string, sandboxPolicyCWD string, useLegacyLandlock bool, allowNetworkForProxy bool) ([]string, error) {
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
	if useLegacyLandlock && !allowNetworkForProxy {
		args = append(args, "--use-legacy-landlock")
	}
	if allowNetworkForProxy {
		args = append(args, "--allow-network-for-proxy")
	}
	args = append(args, "--")
	args = append(args, command...)
	return args, nil
}
