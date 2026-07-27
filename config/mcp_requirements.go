package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const MCPDisabledByRequirements = "disabled by managed requirements"

type MCPServerIdentity struct {
	Command *string
	URL     *string
}

type MCPServerValueMatcher struct {
	Match      string
	Value      string
	Expression string
}

type MCPServerCommandMatcher struct {
	Executable string
	Args       []MCPServerValueMatcher
}

type MCPServerRequirement struct {
	Identity *MCPServerIdentity
	Command  *MCPServerCommandMatcher
	URL      *MCPServerValueMatcher
}

type PluginRequirements struct {
	// A pointer preserves Rust's distinction between no allowlist and an
	// explicitly empty allowlist (deny all).
	MCPServers *map[string]MCPServerRequirement
}

func (r MCPServerValueMatcher) Validate() error {
	switch strings.TrimSpace(r.Match) {
	case "exact", "prefix":
		return nil
	case "regex":
		if _, err := regexp.Compile(r.Expression); err != nil {
			return fmt.Errorf("invalid regex %q: %w", r.Expression, err)
		}
		if _, err := regexp.Compile("^(?:" + r.Expression + ")$"); err != nil {
			return fmt.Errorf("regex %q cannot be used for full-value matching: %w", r.Expression, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported matcher %q", r.Match)
	}
}

func (r MCPServerValueMatcher) Matches(candidate string) bool {
	switch strings.TrimSpace(r.Match) {
	case "exact":
		return candidate == r.Value
	case "prefix":
		return strings.HasPrefix(candidate, r.Value)
	case "regex":
		re, err := regexp.Compile("^(?:" + r.Expression + ")$")
		return err == nil && re.MatchString(candidate)
	default:
		return false
	}
}

func (r MCPServerRequirement) Validate() error {
	variants := 0
	if r.Identity != nil {
		variants++
		if (r.Identity.Command == nil) == (r.Identity.URL == nil) {
			return fmt.Errorf("identity requires exactly one of command or url")
		}
	}
	if r.Command != nil {
		variants++
		if strings.TrimSpace(r.Command.Executable) == "" {
			return fmt.Errorf("command matcher executable is required")
		}
		for index, matcher := range r.Command.Args {
			if err := matcher.Validate(); err != nil {
				return fmt.Errorf("invalid argument matcher at index %d: %w", index, err)
			}
		}
	}
	if r.URL != nil {
		variants++
		if err := r.URL.Validate(); err != nil {
			return err
		}
	}
	if variants != 1 {
		return fmt.Errorf("requirement requires exactly one identity matcher")
	}
	return nil
}

func (r MCPServerRequirement) Matches(command string, args []string, rawURL string) bool {
	if r.Identity != nil {
		if r.Identity.Command != nil {
			return rawURL == "" && command == *r.Identity.Command
		}
		if r.Identity.URL != nil {
			return command == "" && rawURL == *r.Identity.URL
		}
	}
	if r.Command != nil {
		if rawURL != "" || command != r.Command.Executable || len(args) != len(r.Command.Args) {
			return false
		}
		for index := range args {
			if !r.Command.Args[index].Matches(args[index]) {
				return false
			}
		}
		return true
	}
	return r.URL != nil && command == "" && r.URL.Matches(rawURL)
}

func MCPRequirementsFingerprint(requirements *ConfigRequirements) string {
	if requirements == nil {
		return ""
	}
	data, err := json.Marshal(struct {
		MCPServers map[string]MCPServerRequirement
		Plugins    map[string]PluginRequirements
	}{requirements.MCPServers, requirements.Plugins})
	if err != nil {
		return fmt.Sprintf("%#v|%#v", requirements.MCPServers, requirements.Plugins)
	}
	return string(data)
}

func CloneConfigRequirements(requirements *ConfigRequirements) *ConfigRequirements {
	return cloneRequirements(requirements)
}
