package appserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// Environment status values, serialized exactly as Rust's EnvironmentStatus
// (codex-rs/core/src/context/world_state/environment.rs, `snake_case`).
const (
	environmentStatusStarting  = "starting"
	environmentStatusAvailable = "available"
	environmentStatusFailed    = "failed"
)

// localEnvironmentID is Rust's `LOCAL_ENVIRONMENT_ID`, the primary environment
// every turn snapshot contains.
const localEnvironmentID = "local"

// environmentWorldStateEnvironment mirrors Rust's EnvironmentSnapshot. Rust
// serializes the struct and then strips null object fields before persisting,
// so a missing shell or error is simply an absent key here.
type environmentWorldStateEnvironment struct {
	CWD           string `json:"cwd"`
	Status        string `json:"status"`
	Shell         string `json:"shell,omitempty"`
	Error         string `json:"error,omitempty"`
	IsPrimary     bool   `json:"is_primary,omitempty"`
	Filesystem    string `json:"filesystem,omitempty"`
	NetworkAccess string `json:"network_access,omitempty"`
}

// environmentWorldStateSection mirrors Rust's EnvironmentsSnapshot, the
// persisted value of the `environments` world-state section
// (core/src/context/world_state/environment.rs). Go renders the environment
// context from this same value, so the snapshot and the model input cannot
// disagree about what the model has already seen.
type environmentWorldStateSection struct {
	Environments   map[string]environmentWorldStateEnvironment `json:"environments"`
	PerEnvironment bool                                        `json:"per_environment,omitempty"`
	ShellVersion   string                                      `json:"shell_version,omitempty"`
	CurrentDate    string                                      `json:"current_date,omitempty"`
	// Changing the time basis must emit an update even when the date stays
	// the same, so the snapshot records which clock produced the date
	// (core/src/context/world_state/environment.rs #61897).
	CurrentDateIsUTC bool   `json:"current_date_is_utc,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	Network          string `json:"network,omitempty"`
	Filesystem       string `json:"filesystem,omitempty"`
	Subagents        string `json:"subagents,omitempty"`
}

// environmentWorldStateRemovals reports section values that were visible
// before and must be cleared with a `status="unavailable"` marker.
type environmentWorldStateRemovals struct {
	CurrentDate  bool
	ShellVersion bool
}

// environmentWorldStateTransition is the outcome of comparing the current
// environment state with the persisted snapshot: the fragment the model sees
// this turn (empty when nothing changed) and the one-time removal markers.
type environmentWorldStateTransition struct {
	Fragment string
	Removals environmentWorldStateRemovals
}

// environmentWorldStateUpdate is one entry of Rust's EnvironmentUpdate.
type environmentWorldStateUpdate struct {
	environment environmentWorldStateEnvironment
	unavailable bool
}

// renderedEnvironmentWorldState mirrors Rust's RenderedEnvironments: the
// already-diffed values that go inside `<environment_context>`.
type renderedEnvironmentWorldState struct {
	updates             map[string]environmentWorldStateUpdate
	legacySingle        bool
	includePrimary      bool
	shellVersion        string
	shellVersionRemoved bool
	currentDate         string
	currentDateRemoved  bool
	currentDateIsUTC    bool
	timezone            string
	network             string
	filesystem          string
	subagents           string
}

// decodeEnvironmentWorldStateSnapshot restores the stored environment section,
// accepting the legacy Go shape (`currentDate`/`shellVersion`) older stores
// wrote before the section followed Rust's snapshot. An absent or undecodable
// section has no known previous values, which Rust models as
// PreviousSectionState::Absent/Unknown: neither reports a removal.
func decodeEnvironmentWorldStateSnapshot(raw json.RawMessage) (environmentWorldStateSection, bool) {
	if len(raw) == 0 {
		return environmentWorldStateSection{}, false
	}
	var section environmentWorldStateSection
	if err := json.Unmarshal(raw, &section); err != nil {
		return environmentWorldStateSection{}, false
	}
	var legacy struct {
		CurrentDate  string `json:"currentDate"`
		ShellVersion string `json:"shellVersion"`
	}
	_ = json.Unmarshal(raw, &legacy)
	if section.CurrentDate == "" {
		section.CurrentDate = strings.TrimSpace(legacy.CurrentDate)
	}
	if section.ShellVersion == "" {
		section.ShellVersion = strings.TrimSpace(legacy.ShellVersion)
	}
	return section.normalized(), true
}

// normalized trims the recorded strings and guarantees the environments map is
// present, matching the shape Rust serializes after removing null fields.
func (s environmentWorldStateSection) normalized() environmentWorldStateSection {
	s.ShellVersion = strings.TrimSpace(s.ShellVersion)
	s.CurrentDate = strings.TrimSpace(s.CurrentDate)
	s.Timezone = strings.TrimSpace(s.Timezone)
	s.Network = strings.TrimSpace(s.Network)
	s.Filesystem = strings.TrimSpace(s.Filesystem)
	s.Subagents = strings.TrimSpace(s.Subagents)
	environments := make(map[string]environmentWorldStateEnvironment, len(s.Environments))
	for id, environment := range s.Environments {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		environment.CWD = strings.TrimSpace(environment.CWD)
		environment.Status = strings.TrimSpace(environment.Status)
		environment.Shell = strings.TrimSpace(environment.Shell)
		environment.Error = strings.TrimSpace(environment.Error)
		environment.Filesystem = strings.TrimSpace(environment.Filesystem)
		environment.NetworkAccess = strings.TrimSpace(environment.NetworkAccess)
		environments[id] = environment
	}
	s.Environments = environments
	return s
}

// environmentWorldStateSectionForTurn builds Rust's EnvironmentsState from the
// turn's selected environments. The captured environment shell, working
// directory, and (with per_environment_context) its own network and filesystem
// rules are the values the model is about to see.
func (r *RuntimeRouter) environmentWorldStateSectionForTurn(ctx context.Context, params *turn.TurnStartParams, date *time.Time, timezone string, shellVersion string, includeTime bool, utcDate bool, perEnvironment bool, networkRequirements *config.NetworkRequirements) environmentWorldStateSection {
	section := environmentWorldStateSection{
		Environments:   map[string]environmentWorldStateEnvironment{},
		PerEnvironment: perEnvironment,
		ShellVersion:   shellVersion,
	}
	if includeTime {
		section.Timezone = timezone
		if date != nil {
			if utcDate {
				section.CurrentDate = date.UTC().Format("2006-01-02")
				section.CurrentDateIsUTC = true
			} else {
				section.CurrentDate = date.In(time.Local).Format("2006-01-02")
			}
		}
	}
	environments := r.unifiedExecEnvironmentsForTurn(params)
	defaultShellName := r.defaultEnvironmentShellName()
	fallbackShell := ""
	if !perEnvironment && len(environments) <= 1 {
		// The implicit local environment reports the shell exec_command will use.
		fallbackShell = defaultShellName
	}
	var primary *tool.UnifiedExecEnvironment
	if len(environments) > 0 {
		primary = &environments[0]
	}
	for index, environment := range environments {
		id := strings.TrimSpace(environment.ID)
		if id == "" {
			continue
		}
		cwd := strings.TrimSpace(firstNonEmpty(environment.CWD, turnCWD(params), r.services.DefaultCWD, "."))
		entry := environmentWorldStateEnvironment{CWD: cwd, Status: environmentStatusStarting}
		if shellName := strings.TrimSpace(unifiedEnvironmentShellName(environment, fallbackShell)); shellName != "" {
			entry.Status = environmentStatusAvailable
			entry.Shell = shellName
		}
		entry.IsPrimary = len(environments) > 1 && index == 0
		if perEnvironment && environment.PermissionProfile != nil {
			if environment.PermissionProfile.AllowsNetwork() {
				entry.NetworkAccess = "enabled"
			} else {
				entry.NetworkAccess = "restricted"
			}
			roots := environment.WorkspaceRoots
			if len(roots) == 0 {
				roots = []string{cwd}
			}
			if filesystem, err := sandbox.RenderFileSystemContext(environment.PermissionProfile, cwd, roots); err == nil {
				entry.Filesystem = strings.TrimSpace(filesystem)
			}
		}
		section.Environments[id] = entry
	}
	if len(environments) == 0 {
		// Rust's TurnEnvironmentSnapshot always carries the primary local
		// environment; Go's implicit default has no explicit selection.
		entry := environmentWorldStateEnvironment{
			CWD:    strings.TrimSpace(firstNonEmpty(turnCWD(params), r.services.DefaultCWD, ".")),
			Status: environmentStatusStarting,
		}
		if shellName := strings.TrimSpace(defaultShellName); shellName != "" {
			entry.Status = environmentStatusAvailable
			entry.Shell = shellName
		}
		section.Environments[localEnvironmentID] = entry
	}
	if !perEnvironment && primary != nil && primary.PermissionProfile != nil {
		cwd := strings.TrimSpace(firstNonEmpty(primary.CWD, turnCWD(params), r.services.DefaultCWD, "."))
		roots := primary.WorkspaceRoots
		if len(roots) == 0 {
			roots = []string{cwd}
		}
		if filesystem, err := sandbox.RenderFileSystemContext(primary.PermissionProfile, cwd, roots); err == nil {
			section.Filesystem = strings.TrimSpace(filesystem)
		}
	}
	section.Network = environmentWorldStateNetwork(networkRequirements, perEnvironment)
	return section.normalized()
}

// environmentWorldStateSubagents returns the host agent roster the rendered
// environment fragment carries. Rust's WorldState::resolve_host_roster runs
// after the diff selected its updates, so the roster is only read when an
// environment fragment is actually emitted for the model.
func (r *RuntimeRouter) environmentWorldStateSubagents(ctx context.Context, threadID string) string {
	if r == nil {
		return ""
	}
	return strings.Join(r.environmentContextSubagentLines(ctx, strings.TrimSpace(threadID)), "\n")
}

// environmentWorldStateNetwork renders the managed network requirements the
// model sees. With per_environment_context the shared list is labelled as
// thread-wide so it is not mistaken for an environment's own domains.
func environmentWorldStateNetwork(requirements *config.NetworkRequirements, perEnvironment bool) string {
	rendered := strings.TrimSpace(renderThreadNetworkRequirements(requirements))
	if rendered == "" {
		return ""
	}
	if perEnvironment {
		return rendered
	}
	rendered = strings.TrimPrefix(rendered, "<thread_network_requirements>")
	return strings.TrimSuffix(rendered, "</thread_network_requirements>")
}

// renderFull renders Rust's `render_full`: every environment and value, with no
// diff filtering. The compaction window uses it so a missing value is explicit
// rather than reported as unavailable.
func (s environmentWorldStateSection) renderFull(removals environmentWorldStateRemovals, subagents string) string {
	updates := make(map[string]environmentWorldStateUpdate, len(s.Environments))
	for id, environment := range s.Environments {
		updates[id] = environmentWorldStateUpdate{environment: environment}
	}
	rendered := renderedEnvironmentWorldState{
		updates:          updates,
		legacySingle:     !s.PerEnvironment && isLegacySingleEnvironment(s.Environments),
		includePrimary:   len(s.Environments) > 1,
		shellVersion:     s.ShellVersion,
		currentDate:      s.CurrentDate,
		currentDateIsUTC: s.CurrentDateIsUTC,
		timezone:         s.Timezone,
		network:          s.Network,
		filesystem:       s.Filesystem,
		subagents:        strings.TrimSpace(subagents),
	}
	if removals.ShellVersion {
		rendered.shellVersion = ""
		rendered.shellVersionRemoved = true
	}
	if removals.CurrentDate {
		rendered.currentDate = ""
		rendered.currentDateRemoved = true
	}
	return rendered.body()
}

// renderDiff mirrors Rust's EnvironmentsState::render_diff: it returns the
// current snapshot and only what changed since the previous one. An empty
// fragment means the model already saw everything in this turn's state.
func (s environmentWorldStateSection) renderDiff(previous environmentWorldStateSection, known bool, subagents func() string) (environmentWorldStateSection, string) {
	current := s.normalized()
	if !known {
		previous = environmentWorldStateSection{}
	}
	shellVersionAdded := current.ShellVersion != "" && previous.ShellVersion == ""
	formatChanged := current.PerEnvironment != previous.PerEnvironment
	turnContextValuesChanged := formatChanged ||
		current.ShellVersion != previous.ShellVersion ||
		current.CurrentDate != previous.CurrentDate ||
		current.CurrentDateIsUTC != previous.CurrentDateIsUTC ||
		current.Timezone != previous.Timezone ||
		current.Network != previous.Network ||
		current.Filesystem != previous.Filesystem
	multipleEnvironments := len(current.Environments) > 1
	previousMultipleEnvironments := len(previous.Environments) > 1
	updates := make(map[string]environmentWorldStateUpdate, len(current.Environments))
	for _, id := range sortedEnvironmentIDs(current.Environments) {
		environment := current.Environments[id]
		previousEnvironment, exists := previous.Environments[id]
		if exists &&
			!formatChanged &&
			multipleEnvironments == previousMultipleEnvironments &&
			!(shellVersionAdded && previousEnvironment.Shell == "") &&
			environment.sameDiffValue(previousEnvironment) {
			continue
		}
		updates[id] = environmentWorldStateUpdate{environment: environment}
	}
	for _, id := range sortedEnvironmentIDs(previous.Environments) {
		if _, exists := current.Environments[id]; exists {
			continue
		}
		updates[id] = environmentWorldStateUpdate{unavailable: true}
	}
	if len(updates) == 0 && !turnContextValuesChanged {
		return current, ""
	}
	roster := ""
	if subagents != nil {
		roster = strings.TrimSpace(subagents())
	}
	rendered := renderedEnvironmentWorldState{
		updates:             updates,
		legacySingle:        !current.PerEnvironment && isLegacySingleEnvironment(current.Environments) && allEnvironmentUpdatesCurrent(updates),
		includePrimary:      multipleEnvironments || previousMultipleEnvironments,
		shellVersion:        current.ShellVersion,
		shellVersionRemoved: current.ShellVersion == "" && previous.ShellVersion != "",
		currentDate:         current.CurrentDate,
		currentDateRemoved:  current.CurrentDate == "" && previous.CurrentDate != "",
		currentDateIsUTC:    currentDateBasis(current, previous),
		timezone:            current.Timezone,
		network:             current.Network,
		filesystem:          current.Filesystem,
		subagents:           roster,
	}
	return current, rendered.body()
}

// currentDateBasis keeps the previous clock basis when the date disappeared, so
// the unavailable marker names the element the model actually saw (#61897).
func currentDateBasis(current, previous environmentWorldStateSection) bool {
	if current.CurrentDate != "" {
		return current.CurrentDateIsUTC
	}
	return previous.CurrentDateIsUTC
}

// sameDiffValue mirrors Rust's EnvironmentSnapshot::has_same_diff_value: a
// shell that appears or disappears on its own does not restate the
// environment, because the rendered context never showed it.
func (e environmentWorldStateEnvironment) sameDiffValue(other environmentWorldStateEnvironment) bool {
	if e.CWD != other.CWD ||
		e.Status != other.Status ||
		e.Error != other.Error ||
		e.IsPrimary != other.IsPrimary ||
		e.Filesystem != other.Filesystem ||
		e.NetworkAccess != other.NetworkAccess {
		return false
	}
	if e.Shell != "" && other.Shell != "" {
		return e.Shell == other.Shell
	}
	return true
}

func isLegacySingleEnvironment(environments map[string]environmentWorldStateEnvironment) bool {
	if len(environments) != 1 {
		return false
	}
	for _, environment := range environments {
		return environment.Status == environmentStatusAvailable
	}
	return false
}

func allEnvironmentUpdatesCurrent(updates map[string]environmentWorldStateUpdate) bool {
	for _, update := range updates {
		if update.unavailable {
			return false
		}
	}
	return true
}

func sortedEnvironmentIDs(environments map[string]environmentWorldStateEnvironment) []string {
	ids := make([]string, 0, len(environments))
	for id := range environments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedEnvironmentUpdateIDs(updates map[string]environmentWorldStateUpdate) []string {
	ids := make([]string, 0, len(updates))
	for id := range updates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// body renders the fragment content. It starts with a newline so the wrapper
// markers enclose Rust's exact `<environment_context>\n  ...` layout.
func (r renderedEnvironmentWorldState) body() string {
	var b strings.Builder
	b.WriteString("\n")
	if r.legacySingle {
		for _, id := range sortedEnvironmentUpdateIDs(r.updates) {
			update := r.updates[id]
			if update.unavailable {
				continue
			}
			pushEnvironmentValues(&b, update.environment, "  ")
		}
	} else if len(r.updates) > 0 {
		b.WriteString("  <environments>\n")
		for _, id := range sortedEnvironmentUpdateIDs(r.updates) {
			update := r.updates[id]
			b.WriteString("    <environment id=\"")
			pushEnvironmentXMLEscaped(&b, id)
			if update.unavailable {
				b.WriteString("\" status=\"unavailable\" />\n")
				continue
			}
			b.WriteString("\"")
			if r.includePrimary {
				if update.environment.IsPrimary {
					b.WriteString(" primary=\"true\"")
				} else {
					b.WriteString(" primary=\"false\"")
				}
			}
			b.WriteString(">\n")
			pushEnvironmentValues(&b, update.environment, "      ")
			b.WriteString("    </environment>\n")
		}
		b.WriteString("  </environments>\n")
	}
	if r.shellVersionRemoved {
		b.WriteString("  <shell_version status=\"unavailable\" />\n")
	} else {
		pushEnvironmentElement(&b, "shell_version", r.shellVersion)
	}
	dateElement := "current_date"
	if r.currentDateIsUTC {
		dateElement = "current_date_utc"
	}
	if r.currentDateRemoved {
		b.WriteString("  <" + dateElement + " status=\"unavailable\" />\n")
	} else {
		pushEnvironmentElement(&b, dateElement, r.currentDate)
	}
	pushEnvironmentElement(&b, "timezone", r.timezone)
	if r.network != "" {
		b.WriteString("  ")
		b.WriteString(r.network)
		b.WriteByte('\n')
	}
	if r.filesystem != "" {
		b.WriteString("  ")
		b.WriteString(r.filesystem)
		b.WriteByte('\n')
	}
	if r.subagents != "" {
		b.WriteString("  <subagents>\n")
		for _, line := range strings.Split(r.subagents, "\n") {
			b.WriteString("    ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("  </subagents>\n")
	}
	return b.String()
}

// pushEnvironmentValues mirrors Rust's push_environment_values.
func pushEnvironmentValues(b *strings.Builder, environment environmentWorldStateEnvironment, indent string) {
	b.WriteString(indent)
	b.WriteString("<cwd>")
	pushEnvironmentXMLEscaped(b, environment.CWD)
	b.WriteString("</cwd>\n")
	switch environment.Status {
	case environmentStatusStarting, environmentStatusFailed:
		b.WriteString(indent)
		b.WriteString("<status>")
		b.WriteString(environment.Status)
		b.WriteString("</status>\n")
	}
	if environment.Error != "" {
		b.WriteString(indent)
		b.WriteString("<error>")
		pushEnvironmentXMLEscaped(b, environment.Error)
		b.WriteString("</error>\n")
	}
	if environment.Shell != "" {
		b.WriteString(indent)
		b.WriteString("<shell>")
		pushEnvironmentXMLEscaped(b, environment.Shell)
		b.WriteString("</shell>\n")
	}
	if environment.NetworkAccess != "" {
		b.WriteString(indent)
		b.WriteString("<network access=\"")
		b.WriteString(environment.NetworkAccess)
		b.WriteString("\" />\n")
	}
	if environment.Filesystem != "" {
		b.WriteString(indent)
		b.WriteString(environment.Filesystem)
		b.WriteByte('\n')
	}
}

// pushEnvironmentElement mirrors Rust's push_optional_element.
func pushEnvironmentElement(b *strings.Builder, name string, value string) {
	if value == "" {
		return
	}
	b.WriteString("  <")
	b.WriteString(name)
	b.WriteString(">")
	pushEnvironmentXMLEscaped(b, value)
	b.WriteString("</")
	b.WriteString(name)
	b.WriteString(">\n")
}

func pushEnvironmentXMLEscaped(b *strings.Builder, value string) {
	b.WriteString(escapeEnvironmentXML(value))
}

// wrapEnvironmentContext applies the fragment markers.
func wrapEnvironmentContext(body string) string {
	return "<environment_context>" + body + "</environment_context>"
}

// recordEnvironmentWorldState compares the turn's environment state with the
// persisted snapshot, renders the model-visible diff when emitDiff is set, and
// stores the new snapshot. A snapshot the model never saw was never rendered, so
// the persisted value is always the current state of the section, exactly like
// Rust's merge patch after `remove_null_object_fields`.
func (r *RuntimeRouter) recordEnvironmentWorldState(record *session.Record, current environmentWorldStateSection, emitDiff bool, save bool, subagents func() string) (environmentWorldStateTransition, error) {
	transition := environmentWorldStateTransition{}
	if record == nil {
		return transition, nil
	}
	current = current.normalized()
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return transition, err
	}
	previous, known := decodeEnvironmentWorldStateSnapshot(state.Environments)
	if emitDiff {
		var fragment string
		current, fragment = current.renderDiff(previous, known, subagents)
		transition.Fragment = fragment
		if known {
			transition.Removals.CurrentDate = previous.CurrentDate != "" && current.CurrentDate == ""
			transition.Removals.ShellVersion = previous.ShellVersion != "" && current.ShellVersion == ""
		}
	}
	snapshot, err := json.Marshal(current)
	if err != nil {
		return transition, err
	}
	if sameJSONValue(state.Environments, snapshot) {
		return transition, nil
	}
	state.Environments = json.RawMessage(snapshot)
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return transition, err
	}
	if save && r != nil {
		// Ephemeral threads live only in memory under a single writer, so their
		// whole record can be saved without racing a history replacement.
		if r.saveEphemeralThreadRecord(record) {
			return transition, nil
		}
		// Persist only the snapshot. A history replacement (rewind, rollback,
		// or a compaction install) can land while the host roster read is
		// outstanding, and saving this record's stale item list would restore
		// the replaced history (Rust #59531).
		updated, err := r.runtimeUpdateThreadMetadata(record.ID, &session.MetadataPatch{WorldState: record.Metadata.WorldState}, true)
		if err != nil {
			return transition, err
		}
		if updated != nil {
			record.Metadata.WorldState = updated.Metadata.WorldState
		}
	}
	return transition, nil
}
