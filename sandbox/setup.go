package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SetupLevel string

const SetupLevelElevated SetupLevel = "elevated"

type SetupCommand struct {
	Elevated    bool
	User        string
	CurrentUser bool
	CodexHome   string
}

type SetupIdentity struct {
	RealUser  string
	CodexHome string
}

func ParseSetupCommand(args []string) (*SetupCommand, bool, error) {
	if len(args) == 0 || args[0] != "setup" {
		return nil, false, nil
	}
	cmd := &SetupCommand{}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--elevated":
			cmd.Elevated = true
		case "--current-user":
			cmd.CurrentUser = true
		case "--user":
			i++
			if i >= len(args) {
				return nil, true, fmt.Errorf("--user requires a value")
			}
			cmd.User = args[i]
		case "--codex-home":
			i++
			if i >= len(args) {
				return nil, true, fmt.Errorf("--codex-home requires a value")
			}
			cmd.CodexHome = args[i]
		default:
			return nil, true, fmt.Errorf("unknown sandbox setup argument %q", args[i])
		}
	}
	if err := cmd.Validate(); err != nil {
		return nil, true, err
	}
	return cmd, true, nil
}

func (c *SetupCommand) Validate() error {
	if c == nil {
		return fmt.Errorf("command is nil")
	}
	if !c.Elevated {
		return fmt.Errorf("`codex sandbox setup` currently requires --elevated")
	}
	if c.CurrentUser && c.User != "" {
		return fmt.Errorf("--user conflicts with --current-user")
	}
	if !c.CurrentUser && strings.TrimSpace(c.User) == "" {
		return fmt.Errorf("--user or --current-user is required")
	}
	if strings.TrimSpace(c.User) != "" && strings.TrimSpace(c.CodexHome) == "" {
		return fmt.Errorf("--codex-home is required with --user")
	}
	return nil
}

func (c *SetupCommand) SetupLevel() (SetupLevel, error) {
	if c == nil || !c.Elevated {
		return "", fmt.Errorf("`codex sandbox setup` currently requires --elevated")
	}
	return SetupLevelElevated, nil
}

func ResolveSetupIdentity(cmd *SetupCommand, defaultHome string) (*SetupIdentity, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if cmd.CurrentUser {
		user := os.Getenv("USERNAME")
		if user == "" {
			user = os.Getenv("USER")
		}
		if user == "" {
			return nil, fmt.Errorf("failed to determine current user from environment")
		}
		home := cmd.CodexHome
		if strings.TrimSpace(home) == "" {
			home = defaultHome
		}
		if strings.TrimSpace(home) == "" {
			return nil, fmt.Errorf("codex home is required")
		}
		return &SetupIdentity{RealUser: user, CodexHome: filepath.Clean(home)}, nil
	}
	return &SetupIdentity{RealUser: cmd.User, CodexHome: filepath.Clean(cmd.CodexHome)}, nil
}
