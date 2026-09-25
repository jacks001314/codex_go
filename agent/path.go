package agent

// Agent path validation and reference resolution.
//
// Rust parity: codex-rs/protocol/src/agent_path.rs (codex_protocol::AgentPath).
// Paths are absolute (`/root`, or `/root/<name>` for descendants, plus the
// `/morpheus` root) and segment names use only lowercase letters, digits and
// underscores. The `String`-backed type lives in `agent.AgentPath`; this file
// carries the constructors, accessors and validators that mirror the Rust
// inherent methods and the two free validation helpers.

import (
	"fmt"
	"strings"
)

const (
	// AgentPathRoot is the tree's root agent.
	AgentPathRoot AgentPath = "/root"
	// AgentPathMorpheus is the special morpheus root.
	AgentPathMorpheus AgentPath = "/morpheus"

	agentPathRootSegment = "root"
)

// AgentPathRootValue mirrors Rust's `AgentPath::ROOT`.
const AgentPathRootValue = "/root"

// AgentPathMorpheusValue mirrors Rust's `AgentPath::MORPHEUS`.
const AgentPathMorpheusValue = "/morpheus"

// String mirrors Rust's `Display`/`as_str`.
func (p AgentPath) String() string {
	return string(p)
}

// IsAgentPathRoot reports whether the path is the tree root.
func (p AgentPath) IsRoot() bool {
	return p == AgentPathRoot
}

// AgentName returns the final path segment, or "root" for the root path.
func (p AgentPath) AgentName() string {
	if p == "" || p.IsRoot() {
		return agentPathRootSegment
	}
	// Rust takes the last `/`-separated segment and falls back to "root" when it
	// is empty; only validated paths should normally reach this accessor.
	segment := string(p)
	if index := strings.LastIndex(segment, "/"); index >= 0 {
		segment = segment[index+1:]
	}
	if segment == "" {
		return agentPathRootSegment
	}
	return segment
}

// ValidateAgentPath mirrors Rust's validate_absolute_path.
func ValidateAgentPath(path string) error {
	if path == AgentPathMorpheusValue {
		return nil
	}
	stripped, hasPrefix := strings.CutPrefix(path, "/")
	if !hasPrefix {
		return fmt.Errorf("absolute agent paths must start with `/root` or be `/morpheus`")
	}
	segments := strings.Split(stripped, "/")
	if segments[0] != agentPathRootSegment {
		return fmt.Errorf("absolute agent paths must start with `/root` or be `/morpheus`")
	}
	if strings.HasSuffix(stripped, "/") {
		return fmt.Errorf("absolute agent path must not end with `/`")
	}
	for _, segment := range segments[1:] {
		if err := ValidateAgentName(segment); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAgentName mirrors Rust's validate_agent_name.
func ValidateAgentName(name string) error {
	if name == "" {
		return fmt.Errorf("agent_name must not be empty")
	}
	if name == agentPathRootSegment {
		return fmt.Errorf("agent_name `root` is reserved")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("agent_name `%s` is reserved", name)
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("agent_name must not contain `/`")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return fmt.Errorf("agent_name must use only lowercase letters, digits, and underscores")
	}
	return nil
}

// NewAgentPath validates an absolute path.
func NewAgentPath(path string) (AgentPath, error) {
	if err := ValidateAgentPath(path); err != nil {
		return "", err
	}
	return AgentPath(path), nil
}

// Join returns the child path for an agent name.
func (p AgentPath) Join(name string) (AgentPath, error) {
	if err := ValidateAgentName(name); err != nil {
		return "", err
	}
	return NewAgentPath(string(p) + "/" + name)
}

// ResolvePath mirrors Rust's `AgentPath::resolve`: `/root` resolves to the
// root, a `/`-prefixed reference is parsed as an absolute path, and anything
// else is joined onto this path after validating the relative reference. An
// empty reference is rejected (Rust's "agent path must not be empty").
func (p AgentPath) ResolvePath(reference string) (AgentPath, error) {
	if reference == "" {
		return "", fmt.Errorf("agent path must not be empty")
	}
	if reference == AgentPathRootValue {
		return AgentPathRoot, nil
	}
	if strings.HasPrefix(reference, "/") {
		return NewAgentPath(reference)
	}
	if err := validateRelativeAgentReference(reference); err != nil {
		return "", err
	}
	return NewAgentPath(string(p) + "/" + reference)
}

// validateRelativeAgentReference mirrors Rust's validate_relative_reference.
func validateRelativeAgentReference(reference string) error {
	if strings.HasSuffix(reference, "/") {
		return fmt.Errorf("relative agent path must not end with `/`")
	}
	for _, segment := range strings.Split(reference, "/") {
		if err := ValidateAgentName(segment); err != nil {
			return err
		}
	}
	return nil
}
