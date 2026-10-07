package mcp

import (
	"strings"

	managedconfig "codex_go/config"
)

// EnvironmentSelectionState mirrors the configuration state Rust captures in
// `TurnEnvironmentSelection` (#51503): a selected executor is either ready, or
// its owner-supplied configuration is still pending or has failed.
type EnvironmentSelectionState string

const (
	// EnvironmentSelectionReady carries owner-supplied resolved configuration.
	EnvironmentSelectionReady EnvironmentSelectionState = "ready"
	// EnvironmentSelectionPending means the owner will supply configuration later.
	EnvironmentSelectionPending EnvironmentSelectionState = "pending"
	// EnvironmentSelectionFailed means the owner could not supply configuration.
	EnvironmentSelectionFailed EnvironmentSelectionState = "failed"
)

// TurnEnvironmentSelection mirrors Rust
// `codex_protocol::protocol::TurnEnvironmentSelection` (#51503): one captured
// executor selection together with the configuration state observed when it was
// captured. Pending and failed selections are preserved so MCP contributors can
// distinguish an unavailable primary executor from a ready secondary instead of
// silently falling back to the first ready environment.
type TurnEnvironmentSelection struct {
	// EnvironmentID is the selected executor's environment id.
	EnvironmentID string
	// State is the configuration state captured for the selection.
	State EnvironmentSelectionState
	// Error carries the owner failure message for a failed selection.
	Error string
	// McpPolicy is the owner-supplied MCP policy captured with a ready
	// selection (Rust protocol::EnvironmentConfig.mcp_policy, #39335).
	McpPolicy *managedconfig.EnvironmentMCPPolicy
}

// Clone deep-copies a selection so snapshots never alias the parsed turn
// parameters.
func (s TurnEnvironmentSelection) Clone() TurnEnvironmentSelection {
	clone := s
	if s.McpPolicy != nil {
		clone.McpPolicy = s.McpPolicy.Clone()
	}
	return clone
}

// SelectedEnvironments is the ordered snapshot of a thread's captured executor
// selections. Priority order is preserved and pending or failed entries stay in
// the snapshot (Rust #51503).
//
// A nil *SelectedEnvironments means no snapshot was captured at all (threadless
// discovery); a non-nil value with no entries means the thread explicitly
// selected no environments. Rust models the same distinction as `None` versus
// `Some(&[])`.
type SelectedEnvironments struct {
	selections []TurnEnvironmentSelection
}

// NewSelectedEnvironments captures an ordered executor selection snapshot.
func NewSelectedEnvironments(selections []TurnEnvironmentSelection) *SelectedEnvironments {
	clone := make([]TurnEnvironmentSelection, 0, len(selections))
	for _, selection := range selections {
		clone = append(clone, selection.Clone())
	}
	return &SelectedEnvironments{selections: clone}
}

// Len reports how many selections the snapshot holds, including pending and
// failed entries.
func (s *SelectedEnvironments) Len() int {
	if s == nil {
		return 0
	}
	return len(s.selections)
}

// Selections returns a copy of the ordered snapshot.
func (s *SelectedEnvironments) Selections() []TurnEnvironmentSelection {
	if s == nil {
		return nil
	}
	out := make([]TurnEnvironmentSelection, 0, len(s.selections))
	for _, selection := range s.selections {
		out = append(out, selection.Clone())
	}
	return out
}

// EnvironmentIDs returns the selected environment ids in priority order,
// dropping empty ids and duplicates so callers keep the pre-#51503
// `available_environment` list semantics.
func (s *SelectedEnvironments) EnvironmentIDs() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.selections))
	seen := map[string]bool{}
	for _, selection := range s.selections {
		environmentID := strings.TrimSpace(selection.EnvironmentID)
		if environmentID == "" || seen[environmentID] {
			continue
		}
		seen[environmentID] = true
		out = append(out, environmentID)
	}
	return out
}

// Clone deep-copies the snapshot.
func (s *SelectedEnvironments) Clone() *SelectedEnvironments {
	if s == nil {
		return nil
	}
	return NewSelectedEnvironments(s.selections)
}

// Authority derives the MCP availability authority from this snapshot,
// mirroring Rust `McpEnvironmentScope::Selected` (#39335/#46335) with the
// ordered selection as the single source: ready selections without an owner
// policy are unrestricted, ready selections with a policy are restricted, and
// pending or failed selections are unavailable. A nil snapshot yields a nil
// authority so callers keep the legacy unscoped behavior.
func (s *SelectedEnvironments) Authority() *EnvironmentAuthority {
	if s == nil {
		return nil
	}
	authority := &EnvironmentAuthority{
		Scoped:      len(s.selections) > 0,
		Unlimited:   map[string]bool{},
		Restricted:  map[string]*managedconfig.EnvironmentMCPPolicy{},
		Unavailable: map[string]bool{},
	}
	for _, selection := range s.selections {
		environmentID := strings.TrimSpace(selection.EnvironmentID)
		if environmentID == "" {
			continue
		}
		switch selection.State {
		case EnvironmentSelectionPending, EnvironmentSelectionFailed:
			authority.Unavailable[environmentID] = true
		case EnvironmentSelectionReady:
			if selection.McpPolicy != nil {
				authority.Restricted[environmentID] = selection.McpPolicy.Clone()
				continue
			}
			authority.Unlimited[environmentID] = true
		default:
			authority.Unlimited[environmentID] = true
		}
	}
	return authority
}

// MCPServerContributionContext mirrors Rust's `McpServerContributionContext`
// (#51503): the captured step context handed to MCP contributors. Go projects
// plugin-provided MCP servers statically per runtime route instead of invoking a
// per-step contributor callback, so the projection site passes this context and
// publishes the same snapshot on the projected runtime config.
type MCPServerContributionContext struct {
	threadID             string
	selectedEnvironments *SelectedEnvironments
}

// NewMCPServerContributionContext captures the contribution context for one
// thread. A nil snapshot keeps Rust's `None` semantics (threadless discovery);
// pass an explicitly empty snapshot for a thread that selected no environments.
func NewMCPServerContributionContext(threadID string, selected *SelectedEnvironments) MCPServerContributionContext {
	return MCPServerContributionContext{
		threadID:             strings.TrimSpace(threadID),
		selectedEnvironments: selected.Clone(),
	}
}

// ThreadID returns the thread this projection belongs to, or an empty string for
// threadless discovery.
func (c MCPServerContributionContext) ThreadID() string {
	return c.threadID
}

// SelectedEnvironments returns the ordered executor snapshot, or nil when no
// snapshot was captured. An empty snapshot means the thread explicitly has no
// selected environments (Rust `Some(&[])`).
func (c MCPServerContributionContext) SelectedEnvironments() *SelectedEnvironments {
	return c.selectedEnvironments
}

// WithSelectedEnvironments attaches the same environment snapshot used to
// project MCP authority (Rust `McpServerContributionContext::with_selected_environments`).
func (c MCPServerContributionContext) WithSelectedEnvironments(selected *SelectedEnvironments) MCPServerContributionContext {
	c.selectedEnvironments = selected.Clone()
	return c
}
