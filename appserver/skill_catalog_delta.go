package appserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	promptctx "codex_go/prompt"
	"codex_go/session"
)

// Incremental skills catalogs (Rust #57262 `skill_catalog_deltas`) publish only
// the skills the model has not seen yet, charging every emitted row against the
// remaining metadata budget. The snapshot remembers the full inventory and which
// rows were shown, so a removal stays silent and does not refund budget.
const (
	skillCatalogSnapshotVersion = 1
	skillCatalogRootsHeading    = "### Skill roots\nThese mappings replace earlier mappings for the same aliases.\n"
	skillCatalogSkillsHeading   = "### Added or updated skills:\n"
)

type skillCatalogEntrySnapshot struct {
	Fingerprint string `json:"fingerprint"`
	Shown       bool   `json:"shown"`
}

// skillCatalogUsage retains both budget units so switching units cannot refund
// earlier emissions (Rust CatalogUsage).
type skillCatalogUsage struct {
	Tokens     int `json:"tokens"`
	Characters int `json:"characters"`
}

func (u skillCatalogUsage) cost(budget promptctx.SkillMetadataBudget) int {
	if budget.Kind == promptctx.SkillMetadataBudgetTokens {
		return u.Tokens
	}
	return u.Characters
}

// skillCatalogSnapshot is the persisted per-surface catalog state. It mirrors
// Rust CatalogSnapshot, minus the cloud budget allocation Go's fused
// selected-environment catalog does not have.
type skillCatalogSnapshot struct {
	Version          int                                  `json:"version"`
	Entries          map[string]skillCatalogEntrySnapshot `json:"entries"`
	Roots            map[string]string                    `json:"roots"`
	Usage            skillCatalogUsage                    `json:"usage"`
	InstructionsSent bool                                 `json:"instructionsSent"`
	OmitDescriptions bool                                 `json:"omitDescriptions"`
}

func decodeSkillCatalogSnapshot(raw json.RawMessage) *skillCatalogSnapshot {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var snapshot skillCatalogSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil
	}
	if snapshot.Version != skillCatalogSnapshotVersion {
		return nil
	}
	return &snapshot
}

func encodeSkillCatalogSnapshot(snapshot *skillCatalogSnapshot) (json.RawMessage, error) {
	if snapshot == nil {
		return nil, nil
	}
	if snapshot.Version == 0 {
		snapshot.Version = skillCatalogSnapshotVersion
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func newSkillCatalogSnapshot() *skillCatalogSnapshot {
	return &skillCatalogSnapshot{
		Version: skillCatalogSnapshotVersion,
		Entries: map[string]skillCatalogEntrySnapshot{},
		Roots:   map[string]string{},
	}
}

func skillCatalogUsageCost(snapshot *skillCatalogSnapshot, budget promptctx.SkillMetadataBudget) int {
	if snapshot == nil {
		return 0
	}
	return snapshot.Usage.cost(budget)
}

// skillCatalogDeltaResult is one surface's contribution for this turn.
type skillCatalogDeltaResult struct {
	Body     string
	Report   *promptctx.SkillRenderReport
	Snapshot *skillCatalogSnapshot
}

// buildSkillCatalogDelta mirrors Rust catalog_delta::skills_delta_section: a
// surface with no usable snapshot publishes a full listing; otherwise only
// added and edited rows are appended, charged against the shared remaining
// budget.
func buildSkillCatalogDelta(source string, available *promptctx.AvailableSkills, includeUsage bool, previous *skillCatalogSnapshot, remaining *int, budget promptctx.SkillMetadataBudget) skillCatalogDeltaResult {
	if available == nil {
		return skillCatalogDeltaResult{Snapshot: newSkillCatalogSnapshot()}
	}
	if previous == nil {
		return buildInitialSkillCatalogDelta(source, available, includeUsage)
	}
	return buildSkillCatalogUpdate(source, available, includeUsage, previous, remaining, budget)
}

func buildInitialSkillCatalogDelta(source string, available *promptctx.AvailableSkills, includeUsage bool) skillCatalogDeltaResult {
	snapshot := newSkillCatalogSnapshot()
	snapshot.OmitDescriptions = available.OmitDescriptions
	usage := measureSkillCatalogUsage(func(budget promptctx.SkillMetadataBudget) int {
		cost := 0
		for _, line := range available.SkillLines {
			cost += promptctx.SkillMetadataLineCost(budget, line)
		}
		if len(available.SkillRootLines) > 0 {
			cost += promptctx.AliasedSkillMetadataOverheadCost(budget, available.SkillRootLines, available.OmitDescriptions)
		}
		return cost
	})
	snapshot.Usage = usage
	snapshot.InstructionsSent = true
	for _, entry := range available.Entries {
		snapshot.Entries[entry.ID] = skillCatalogEntrySnapshot{Fingerprint: entry.Fingerprint, Shown: entry.Shown}
		if entry.RootName != "" {
			snapshot.Roots[entry.RootName] = entry.RootValue
		}
	}
	return skillCatalogDeltaResult{
		Body:     "\n## " + source + " skills\n" + available.Body,
		Report:   available.Report,
		Snapshot: snapshot,
	}
}

func buildSkillCatalogUpdate(source string, available *promptctx.AvailableSkills, includeUsage bool, previous *skillCatalogSnapshot, remaining *int, budget promptctx.SkillMetadataBudget) skillCatalogDeltaResult {
	refreshUsage := previous.InstructionsSent && includeUsage && previous.OmitDescriptions != available.OmitDescriptions
	rendered := make([]promptctx.SkillCatalogEntryRender, len(available.Entries))
	copy(rendered, available.Entries)
	// Rust iterates the rendered catalog's BTreeMap, so updates publish rows in
	// entry-id order rather than render order.
	sort.SliceStable(rendered, func(i int, j int) bool { return rendered[i].ID < rendered[j].ID })

	draft := newSkillCatalogUpdateDraft(source, available, previous.InstructionsSent)
	report := &promptctx.SkillRenderReport{}
	nextEntries := make(map[string]skillCatalogEntrySnapshot, len(rendered))
	for _, entry := range rendered {
		old, exists := previous.Entries[entry.ID]
		shown := exists && old.Shown
		if !exists || (old.Shown && old.Fingerprint != entry.Fingerprint) {
			report.TotalCount++
			if draft.appendEntry(entry, previous, remaining, budget, available.OmitDescriptions, includeUsage) {
				shown = true
				report.IncludedCount++
			} else {
				report.OmittedCount++
			}
		}
		nextEntries[entry.ID] = skillCatalogEntrySnapshot{Fingerprint: entry.Fingerprint, Shown: shown}
	}

	body := draft.body()
	if refreshUsage {
		instructions := promptctx.SkillUsageInstructions(len(available.SkillRootLines) > 0, available.OmitDescriptions)
		if body == "" {
			body = "\n## " + source + " skills update\n"
		}
		body += "\n### How to use skills\n" + instructions + "\n"
	}
	previous.OmitDescriptions = available.OmitDescriptions
	previous.Entries = nextEntries
	if body != "" {
		previous.InstructionsSent = true
	}
	return skillCatalogDeltaResult{Body: body, Report: report, Snapshot: previous}
}

func measureSkillCatalogUsage(cost func(promptctx.SkillMetadataBudget) int) skillCatalogUsage {
	return skillCatalogUsage{
		Tokens:     cost(promptctx.SkillMetadataBudget{Kind: promptctx.SkillMetadataBudgetTokens, Limit: promptctx.MaxConfiguredSkillMetadataTokenBudget}),
		Characters: cost(promptctx.SkillMetadataBudget{Kind: promptctx.SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudgetForCost}),
	}
}

// DefaultSkillMetadataCharBudgetForCost only supplies a unit-carrying budget to
// the cost measurement helpers, which ignore the limit.
const DefaultSkillMetadataCharBudgetForCost = 1 << 20

// skillCatalogUpdateDraft accumulates the rows one update message will carry.
// It mirrors Rust catalog_delta::UpdateDraft, including the first-row and
// first-root-mapping overhead charges.
type skillCatalogUpdateDraft struct {
	source           string
	firstListing     bool
	omitDescriptions bool
	rootLines        []string
	skillLines       []string
}

func newSkillCatalogUpdateDraft(source string, available *promptctx.AvailableSkills, instructionsSent bool) *skillCatalogUpdateDraft {
	return &skillCatalogUpdateDraft{
		source:           source,
		firstListing:     !instructionsSent,
		omitDescriptions: available.OmitDescriptions,
	}
}

func (d *skillCatalogUpdateDraft) appendEntry(entry promptctx.SkillCatalogEntryRender, snapshot *skillCatalogSnapshot, remaining *int, budget promptctx.SkillMetadataBudget, omitDescriptions bool, includeUsage bool) bool {
	var newRootName, newRootValue string
	if entry.RootName != "" && snapshot.Roots[entry.RootName] != entry.RootValue {
		newRootName = entry.RootName
		newRootValue = entry.RootValue
	}
	newRootLine := ""
	if newRootName != "" {
		newRootLine = fmt.Sprintf("- `%s` = `%s`", newRootName, newRootValue)
	}
	cost := measureSkillCatalogUsage(func(budget promptctx.SkillMetadataBudget) int {
		total := promptctx.SkillMetadataLineCost(budget, entry.Line)
		if !d.firstListing && len(d.skillLines) == 0 {
			total += promptctx.SkillMetadataCost(budget, "\n## "+d.source+" skills update\n")
			total += promptctx.SkillMetadataCost(budget, skillCatalogSkillsHeading)
		}
		if newRootLine != "" {
			if d.firstListing && len(d.rootLines) == 0 {
				total += promptctx.AliasedSkillMetadataOverheadCost(budget, []string{newRootLine}, omitDescriptions)
			} else if !d.firstListing && len(d.rootLines) == 0 {
				total += promptctx.SkillMetadataCost(budget, skillCatalogRootsHeading)
				total += promptctx.SkillMetadataLineCost(budget, newRootLine)
			} else {
				total += promptctx.SkillMetadataLineCost(budget, newRootLine)
			}
		}
		return total
	})
	charge := cost.cost(budget)
	if remaining != nil {
		if charge > *remaining {
			return false
		}
		*remaining -= charge
	}
	snapshot.Usage.Tokens += cost.Tokens
	snapshot.Usage.Characters += cost.Characters
	if newRootLine != "" {
		snapshot.Roots[newRootName] = newRootValue
		d.rootLines = append(d.rootLines, newRootLine)
	}
	d.skillLines = append(d.skillLines, entry.Line)
	return true
}

func (d *skillCatalogUpdateDraft) body() string {
	if len(d.skillLines) == 0 {
		return ""
	}
	if d.firstListing {
		return "\n## " + d.source + " skills\n" + promptctx.RenderSkillInstructionsBody(d.rootLines, d.skillLines, true, d.omitDescriptions)
	}
	heading := "\n## " + d.source + " skills update\n"
	if len(d.rootLines) > 0 {
		heading += skillCatalogRootsHeading + strings.Join(d.rootLines, "\n") + "\n"
	}
	heading += skillCatalogSkillsHeading + strings.Join(d.skillLines, "\n") + "\n"
	return heading
}

// incrementalSkillCatalogBodies builds this turn's incremental skill catalog
// sections and persists their snapshots (Rust #57262
// world_state_catalogs::render_world_state_sections). Executor rows are charged
// before host rows, matching Rust's section order, while the returned bodies
// keep Go's established host-then-executor developer-message order.
func (r *RuntimeRouter) incrementalSkillCatalogBodies(threadID string, budget promptctx.SkillMetadataBudget, includeUsage bool, hostAvailable *promptctx.AvailableSkills, executorAvailable *promptctx.AvailableSkills) (string, string, error) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return "", "", err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return "", "", err
	}
	previousSkills := decodeSkillCatalogSnapshot(state.SkillsCatalog)
	previousHost := decodeSkillCatalogSnapshot(state.HostSkillsCatalog)
	used := skillCatalogUsageCost(previousSkills, budget) + skillCatalogUsageCost(previousHost, budget)
	remaining := budget.Limit - used
	if remaining < 0 {
		remaining = 0
	}
	executorResult := buildSkillCatalogDelta("Selected-environment", executorAvailable, includeUsage, previousSkills, &remaining, budget)
	hostResult := buildSkillCatalogDelta("Host", hostAvailable, includeUsage, previousHost, &remaining, budget)
	for _, result := range []skillCatalogDeltaResult{executorResult, hostResult} {
		if warning := promptctx.SkillRenderWarningWithPolicy(result.Report, budget, true); warning != nil && strings.TrimSpace(*warning) != "" {
			r.notify(NotificationWarning, &WarningNotification{
				ThreadID: stringPtrIfNotEmpty(threadID),
				Message:  strings.TrimSpace(*warning),
			})
		}
	}
	state.SkillsCatalog, err = encodeSkillCatalogSnapshot(executorResult.Snapshot)
	if err != nil {
		return "", "", err
	}
	state.HostSkillsCatalog, err = encodeSkillCatalogSnapshot(hostResult.Snapshot)
	if err != nil {
		return "", "", err
	}
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return "", "", err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return "", "", err
	}
	return hostResult.Body, executorResult.Body, nil
}

// clearSkillCatalogSnapshots restarts incremental skill catalogs after a
// compaction (Rust #57262: compaction starts a fresh listing and budget).
func clearSkillCatalogSnapshots(record *session.Record) error {
	if record == nil || len(record.Metadata.WorldState) == 0 {
		return nil
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return err
	}
	if len(state.SkillsCatalog) == 0 && len(state.HostSkillsCatalog) == 0 {
		return nil
	}
	state.SkillsCatalog = nil
	state.HostSkillsCatalog = nil
	encoded, err := session.EncodeWorldState(state)
	if err != nil {
		return err
	}
	record.Metadata.WorldState = encoded
	return nil
}
