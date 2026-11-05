package state

import (
	"errors"
	"fmt"
	"math"

	"codex_go/retainedctx"
	"codex_go/utils"
)

// GuardianElasticImageTokenReservation mirrors Rust's IMAGE_TOKEN_RESERVATION:
// an opaque image is charged conservatively and is never split.
const GuardianElasticImageTokenReservation = 10_000

// GuardianElasticPriorityTool names the optional retention priority that
// selects the transcript tool allocation group.
const GuardianElasticPriorityTool = "tool"

// GuardianElasticRetention marks one item required or optional. Required and
// historical evidence is never removed by elastic allocation.
type GuardianElasticRetention struct {
	Optional bool
	Priority string
}

// GuardianElasticRequired returns the retention of evidence that must survive.
func GuardianElasticRequired() GuardianElasticRetention {
	return GuardianElasticRetention{}
}

// GuardianElasticOptional returns the retention of one optional entry.
func GuardianElasticOptional(priority string) GuardianElasticRetention {
	return GuardianElasticRetention{Optional: true, Priority: priority}
}

// GuardianElasticItem is one whole optional or required entry. TokenCost is the
// caller's measured delivery cost; zero derives a conservative estimate. A
// non-empty ImageURL marks an inline image, which is kept whole.
type GuardianElasticItem struct {
	Text      string
	ImageURL  string
	TokenCost int
	Retention GuardianElasticRetention
	// Exact marks content delivered verbatim (the planned action JSON), which
	// entry preparation never shortens and allocation charges globally.
	Exact bool
	// Protected marks host framing, authorization warnings and section
	// boundaries that stay intact when elastic entry caps shorten supporting
	// evidence.
	Protected bool
	// Incomplete marks an entry whose source was shortened during elastic
	// preparation.
	Incomplete bool
	// Source identifies the complete original revision this transcript entry
	// proves was delivered.
	Source *retainedctx.RetainedSource
}

func (item GuardianElasticItem) protected() bool {
	return item.Protected || item.Exact
}

// GuardianElasticSectionOutput is one composed section, named for the source
// section that owns its evidence. Native sections wrap a single message and
// are all-or-nothing.
type GuardianElasticSectionOutput struct {
	ID     string
	Items  []GuardianElasticItem
	Native bool
}

// GuardianElasticContext is the composed Guardian input before allocation.
type GuardianElasticContext struct {
	Sections    []GuardianElasticSectionOutput
	Truncated   bool
	Truncations []GuardianElasticTruncation
}

// GuardianElasticRequestBudget is the complete input allowance and the context
// already carried by the reviewer.
type GuardianElasticRequestBudget struct {
	MaxInputTokens        int
	ExistingContextTokens int
}

// ErrGuardianElasticEvidenceLimit reports required evidence that cannot fit the
// complete-request budget or a section minimum.
var ErrGuardianElasticEvidenceLimit = errors.New("guardian elastic evidence does not fit the request budget")

// GuardianElasticBudgetError names the section whose required evidence did not
// fit.
type GuardianElasticBudgetError struct {
	Section string
}

func (e GuardianElasticBudgetError) Error() string {
	return fmt.Sprintf("%s: %s", ErrGuardianElasticEvidenceLimit, e.Section)
}

func (e GuardianElasticBudgetError) Unwrap() error { return ErrGuardianElasticEvidenceLimit }

// EstimatedTokens is the conservative complete-section estimate.
func (c GuardianElasticContext) EstimatedTokens() int {
	total := 0
	for _, section := range c.Sections {
		total += guardianElasticSectionTokens(section)
	}
	return total
}

// GuardianElasticInstructionDeduplicator applies delivery credit to retained
// instructions after a selection. It returns true when it removed evidence and
// the caller should allocate again with the released budget.
type GuardianElasticInstructionDeduplicator func(*GuardianElasticContext, map[[2]int]bool) (bool, error)

// EnforceGuardianElasticBudget mirrors Rust
// PreparedElasticContext::enforce_budget (#59401): section minimums are fixed,
// only headroom up to each section maximum is scaled, Required records precede
// Optional records, and protected content reserves global space before
// selection.
func EnforceGuardianElasticBudget(
	context GuardianElasticContext,
	config GuardianElasticBudgetConfig,
	budget GuardianElasticRequestBudget,
	omissionNotice string,
) (GuardianElasticContext, error) {
	return enforceGuardianElasticBudget(context, config, budget, omissionNotice, nil)
}

// EnforceGuardianElasticBudgetWithDedup reallocates when selected transcript
// proof credits retained instructions, preserving the same proof on refill.
func EnforceGuardianElasticBudgetWithDedup(
	context GuardianElasticContext,
	config GuardianElasticBudgetConfig,
	budget GuardianElasticRequestBudget,
	omissionNotice string,
	deduplicate GuardianElasticInstructionDeduplicator,
) (GuardianElasticContext, error) {
	return enforceGuardianElasticBudget(context, config, budget, omissionNotice, deduplicate)
}

func enforceGuardianElasticBudget(
	context GuardianElasticContext,
	config GuardianElasticBudgetConfig,
	budget GuardianElasticRequestBudget,
	omissionNotice string,
	deduplicate GuardianElasticInstructionDeduplicator,
) (GuardianElasticContext, error) {
	if err := config.Validate(); err != nil {
		return GuardianElasticContext{}, err
	}
	sections := make([]GuardianElasticSectionOutput, 0, len(context.Sections))
	for _, section := range context.Sections {
		if section.ID != "budget_omission" {
			sections = append(sections, section)
		}
	}
	context.Sections = sections
	remaining := budget.MaxInputTokens - budget.ExistingContextTokens
	if remaining < 0 {
		return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
	}
	pinned := map[[2]int]bool{}
	minimumsFunded := false
	selected := map[[2]int]bool{}
	needsNotice := false
	var minimumsFit bool
	for {
		demand, err := collectGuardianElasticDemand(context)
		if err != nil {
			return GuardianElasticContext{}, err
		}
		needsNotice = len(context.Truncations) > 0 || demand.exceedsMaximum(config) || context.EstimatedTokens() > remaining
		noticeTokens := 0
		if needsNotice {
			noticeTokens = guardianElasticSectionTokens(guardianElasticOmissionSection(omissionNotice))
		}
		allowance := remaining - demand.protectedTokens - noticeTokens
		if allowance < 0 {
			return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
		}
		selected, minimumsFit, err = allocateGuardianElasticItems(&demand, config, allowance, pinned)
		if err != nil {
			return GuardianElasticContext{}, err
		}
		minimumsFunded = minimumsFunded || minimumsFit
		if deduplicate == nil {
			if !minimumsFunded {
				return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
			}
			break
		}
		removed, err := deduplicate(&context, selected)
		if err != nil {
			return GuardianElasticContext{}, err
		}
		if !removed {
			if !minimumsFunded {
				return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
			}
			break
		}
		for sectionIndex, section := range context.Sections {
			if section.Native || section.ID != "conversation_transcript" {
				continue
			}
			for itemIndex, item := range section.Items {
				if !item.Retention.Optional && selected[[2]int{sectionIndex, itemIndex}] {
					pinned[[2]int{sectionIndex, itemIndex}] = true
				}
			}
		}
	}
	retainGuardianElasticSelection(&context, selected)
	if needsNotice || len(context.Truncations) > 0 {
		context.Sections = append(context.Sections, guardianElasticOmissionSection(omissionNotice))
	}
	if context.EstimatedTokens() > remaining {
		return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
	}
	return context, nil
}

// RequiredTranscriptSources returns complete originals proved by required
// transcript entries that survived selection.
func (c GuardianElasticContext) RequiredTranscriptSources(selected map[[2]int]bool) []retainedctx.RetainedSource {
	sources := make([]retainedctx.RetainedSource, 0)
	for sectionIndex, section := range c.Sections {
		if section.Native || section.ID != "conversation_transcript" {
			continue
		}
		for itemIndex, item := range section.Items {
			if item.Retention.Optional || !selected[[2]int{sectionIndex, itemIndex}] ||
				item.Source == nil || !item.Source.Complete {
				continue
			}
			sources = append(sources, *item.Source)
		}
	}
	return sources
}

func guardianElasticOmissionSection(notice string) GuardianElasticSectionOutput {
	return GuardianElasticSectionOutput{
		ID: "budget_omission",
		Items: []GuardianElasticItem{{
			Text:      notice,
			Retention: GuardianElasticRequired(),
			Protected: true,
		}},
	}
}

type guardianElasticCandidate struct {
	sectionIndex int
	itemIndex    int
	cost         int
}

type guardianElasticSectionDemand struct {
	requiredItems []guardianElasticCandidate
	optionalItems []guardianElasticCandidate
	selectedItems []guardianElasticCandidate
	totalCost     int
	maximum       int
	quota         int
	used          int
}

type guardianElasticDemand struct {
	protectedTokens int
	sections        map[GuardianElasticSection]*guardianElasticSectionDemand
	contentCounts   []int
	userContent     []bool
	minimumsFit     bool
}

// guardianElasticAllocationSection mirrors Rust allocation_section: the
// transcript's tool-priority entries use the tool group while its other entries
// use the message group.
func guardianElasticAllocationSection(section string, retention GuardianElasticRetention) (GuardianElasticSection, error) {
	if section == "conversation_transcript" {
		if retention.Optional && retention.Priority == GuardianElasticPriorityTool {
			return GuardianElasticSectionTranscriptTools, nil
		}
		return GuardianElasticSectionTranscriptMessages, nil
	}
	switch section {
	case "root_conversation":
		return GuardianElasticSectionRootConversationMessages, nil
	case "retained_user_instructions":
		return GuardianElasticSectionRetainedUserInstructions, nil
	case "trusted_user_answers":
		return GuardianElasticSectionTrustedUserAnswers, nil
	case "sender_user_messages":
		return GuardianElasticSectionSenderUserMessages, nil
	case "planned_action":
		return GuardianElasticSectionPlannedAction, nil
	case "previous_reviews":
		return GuardianElasticSectionPreviousReviews, nil
	case "trusted_tool":
		return GuardianElasticSectionTrustedTools, nil
	case "trusted_skills":
		return GuardianElasticSectionTrustedSkills, nil
	case "transcript_images":
		return GuardianElasticSectionTranscriptImages, nil
	case "node_repl_evidence":
		return GuardianElasticSectionNodeReplEvidence, nil
	case "permissions":
		return GuardianElasticSectionPermissionContext, nil
	default:
		return "", fmt.Errorf("unsupported guardian elastic section %q", section)
	}
}

func collectGuardianElasticDemand(context GuardianElasticContext) (guardianElasticDemand, error) {
	demand := guardianElasticDemand{
		sections:      map[GuardianElasticSection]*guardianElasticSectionDemand{},
		contentCounts: make([]int, len(context.Sections)),
		userContent:   make([]bool, len(context.Sections)),
	}
	for sectionIndex, section := range context.Sections {
		protectedCount := 0
		if !section.Native {
			demand.userContent[sectionIndex] = true
		}
		add := func(itemIndex int, retention GuardianElasticRetention, cost int) error {
			group, err := guardianElasticAllocationSection(section.ID, retention)
			if err != nil {
				return err
			}
			entry := demand.sections[group]
			if entry == nil {
				entry = &guardianElasticSectionDemand{}
				demand.sections[group] = entry
			}
			candidate := guardianElasticCandidate{sectionIndex: sectionIndex, itemIndex: itemIndex, cost: cost}
			if retention.Optional {
				entry.optionalItems = append(entry.optionalItems, candidate)
			} else {
				entry.requiredItems = append(entry.requiredItems, candidate)
			}
			return nil
		}
		if section.Native {
			cost := guardianElasticSectionTokens(section)
			retention := GuardianElasticRequired()
			if len(section.Items) > 0 {
				retention = section.Items[0].Retention
			}
			itemsProtected := false
			for _, item := range section.Items {
				itemsProtected = itemsProtected || item.protected()
			}
			if section.ID == "intro" || itemsProtected {
				demand.protectedTokens += cost
				continue
			}
			if err := add(0, retention, cost); err != nil {
				return guardianElasticDemand{}, err
			}
			continue
		}
		for itemIndex, item := range section.Items {
			cost := guardianElasticItemTokens(item)
			if section.ID == "intro" || item.protected() {
				protectedCount++
				demand.protectedTokens += cost
				continue
			}
			if err := add(itemIndex, item.Retention, cost); err != nil {
				return guardianElasticDemand{}, err
			}
		}
		demand.protectedTokens += guardianElasticContentFramingTokens(protectedCount)
	}
	for _, entry := range demand.sections {
		counts := demand.quotaCounts()
		entry.totalCost = 0
		for _, item := range entry.requiredItems {
			entry.totalCost += demand.selectCandidate(item, counts)
		}
		for _, item := range entry.optionalItems {
			entry.totalCost += demand.selectCandidate(item, counts)
		}
	}
	return demand, nil
}

func (demand *guardianElasticDemand) quotaCounts() []int {
	counts := make([]int, len(demand.contentCounts))
	for index, nonNative := range demand.userContent {
		if nonNative {
			counts[index] = 1
		}
	}
	return counts
}

func (demand *guardianElasticDemand) candidateCost(candidate guardianElasticCandidate, counts []int) int {
	cost := candidate.cost
	if !demand.userContent[candidate.sectionIndex] {
		return cost
	}
	count := counts[candidate.sectionIndex]
	return cost + guardianElasticContentFramingTokens(count+1) - guardianElasticContentFramingTokens(count)
}

func (demand *guardianElasticDemand) selectCandidate(candidate guardianElasticCandidate, counts []int) int {
	cost := demand.candidateCost(candidate, counts)
	if demand.userContent[candidate.sectionIndex] {
		counts[candidate.sectionIndex]++
	}
	return cost
}

func (demand *guardianElasticDemand) exceedsMaximum(config GuardianElasticBudgetConfig) bool {
	for section, entry := range demand.sections {
		maximum := config.SectionLimits(section).MaxTotalSectionTokens
		if maximum != nil && entry.totalCost > *maximum {
			return true
		}
	}
	return false
}

// guardianElasticSectionOrder follows the Rust ElasticSection declaration order
// used by BTreeMap iteration. Allocation order is observable when groups share
// leftover capacity.
var guardianElasticSectionOrder = []GuardianElasticSection{
	GuardianElasticSectionTranscriptMessages,
	GuardianElasticSectionTranscriptTools,
	GuardianElasticSectionRootConversationMessages,
	GuardianElasticSectionRetainedUserInstructions,
	GuardianElasticSectionTrustedUserAnswers,
	GuardianElasticSectionSenderUserMessages,
	GuardianElasticSectionPlannedAction,
	GuardianElasticSectionPreviousReviews,
	GuardianElasticSectionTrustedTools,
	GuardianElasticSectionTrustedSkills,
	GuardianElasticSectionTranscriptImages,
	GuardianElasticSectionNodeReplEvidence,
	GuardianElasticSectionPermissionContext,
}

func allocateGuardianElasticItems(
	demand *guardianElasticDemand,
	config GuardianElasticBudgetConfig,
	allowance int,
	pinned map[[2]int]bool,
) (map[[2]int]bool, bool, error) {
	if pinned == nil {
		pinned = map[[2]int]bool{}
	}
	counts := append([]int(nil), demand.contentCounts...)
	selected := map[[2]int]bool{}
	remaining := allowance
	for _, group := range guardianElasticSectionOrder {
		entry := demand.sections[group]
		if entry == nil {
			continue
		}
		limits := config.SectionLimits(group)
		entry.maximum = math.MaxInt
		if limits.MaxTotalSectionTokens != nil {
			entry.maximum = *limits.MaxTotalSectionTokens
		}
		quotaCounts := demand.quotaCounts()
		for _, item := range entry.requiredItems {
			if !pinned[[2]int{item.sectionIndex, item.itemIndex}] {
				continue
			}
			cost := demand.selectCandidate(item, counts)
			if cost > remaining {
				return nil, false, GuardianElasticBudgetError{Section: "request_budget"}
			}
			remaining -= cost
			entry.used += demand.selectCandidate(item, quotaCounts)
			entry.selectedItems = append(entry.selectedItems, item)
			selected[[2]int{item.sectionIndex, item.itemIndex}] = true
		}
		if entry.used > entry.maximum {
			return nil, false, GuardianElasticBudgetError{Section: "request_budget"}
		}
		entry.quota = min(limits.MinTotalSectionTokens, entry.totalCost)
		if entry.quota < entry.used {
			entry.quota = entry.used
		}
	}

	reserved := 0
	for _, entry := range demand.sections {
		reserved += entry.quota - entry.used
	}
	minimumsFit := reserved <= remaining
	growthBudget := remaining - reserved
	if growthBudget < 0 {
		growthBudget = 0
	}
	headroom := 0
	for _, entry := range demand.sections {
		headroom += min(entry.totalCost, entry.maximum) - entry.quota
	}
	for _, group := range guardianElasticSectionOrder {
		entry := demand.sections[group]
		if entry == nil {
			continue
		}
		growth := min(entry.totalCost, entry.maximum) - entry.quota
		if growth <= 0 || headroom <= 0 {
			continue
		}
		entry.quota += int(int64(growth) * int64(min(growthBudget, headroom)) / int64(headroom))
	}
	for _, group := range guardianElasticSectionOrder {
		entry := demand.sections[group]
		if entry == nil {
			continue
		}
		if err := entry.allocate(demand, &remaining, counts, selected, pinned); err != nil {
			return nil, false, err
		}
	}
	roundingHeadroom := 0
	for _, nonNative := range demand.userContent {
		if nonNative {
			roundingHeadroom++
		}
	}
	for {
		var chosen *guardianElasticSectionDemand
		for _, group := range guardianElasticSectionOrder {
			entry := demand.sections[group]
			if entry == nil {
				continue
			}
			target := min(entry.used+remaining+roundingHeadroom, entry.maximum, entry.totalCost)
			if entry.quota >= target {
				continue
			}
			if chosen == nil || entry.used < chosen.used {
				chosen = entry
			}
		}
		if chosen == nil {
			break
		}
		chosen.quota = min(chosen.used+remaining+roundingHeadroom, chosen.maximum, chosen.totalCost)
		if err := chosen.allocate(demand, &remaining, counts, selected, pinned); err != nil {
			return nil, false, err
		}
	}
	demand.minimumsFit = minimumsFit
	return selected, minimumsFit, nil
}

func (entry *guardianElasticSectionDemand) allocate(
	demand *guardianElasticDemand,
	remaining *int,
	counts []int,
	selected map[[2]int]bool,
	pinned map[[2]int]bool,
) error {
	for _, item := range entry.selectedItems {
		delete(selected, [2]int{item.sectionIndex, item.itemIndex})
		*remaining += item.cost
		if demand.userContent[item.sectionIndex] {
			*remaining += guardianElasticContentFramingTokens(counts[item.sectionIndex]) -
				guardianElasticContentFramingTokens(counts[item.sectionIndex]-1)
			counts[item.sectionIndex]--
		}
	}
	entry.selectedItems = nil
	entry.used = 0
	quotaCounts := demand.quotaCounts()
	keep := func(item guardianElasticCandidate) bool {
		if demand.candidateCost(item, quotaCounts) > entry.quota-entry.used ||
			demand.candidateCost(item, counts) > *remaining {
			return false
		}
		cost := demand.selectCandidate(item, counts)
		entry.used += demand.selectCandidate(item, quotaCounts)
		*remaining -= cost
		selected[[2]int{item.sectionIndex, item.itemIndex}] = true
		entry.selectedItems = append(entry.selectedItems, item)
		return true
	}
	for _, item := range entry.requiredItems {
		if pinned[[2]int{item.sectionIndex, item.itemIndex}] && !keep(item) {
			return GuardianElasticBudgetError{Section: "request_budget"}
		}
	}
	for index := len(entry.requiredItems) - 1; index >= 0; index-- {
		item := entry.requiredItems[index]
		if pinned[[2]int{item.sectionIndex, item.itemIndex}] {
			continue
		}
		if !keep(item) {
			break
		}
	}
	for index := len(entry.optionalItems) - 1; index >= 0; index-- {
		keep(entry.optionalItems[index])
	}
	return nil
}

func retainGuardianElasticSelection(context *GuardianElasticContext, selected map[[2]int]bool) {
	for sectionIndex := range context.Sections {
		section := &context.Sections[sectionIndex]
		if section.ID == "intro" {
			continue
		}
		if section.Native {
			if len(section.Items) == 0 || section.Items[0].protected() || selected[[2]int{sectionIndex, 0}] {
				continue
			}
			context.Truncations = append(context.Truncations, GuardianElasticTruncation{
				Component:     section.ID,
				OriginalBytes: len(section.Items[0].Text),
			})
			section.Items = nil
			context.Truncated = true
			continue
		}
		kept := make([]GuardianElasticItem, 0, len(section.Items))
		originalItems := section.Items
		for itemIndex, item := range originalItems {
			if item.protected() || selected[[2]int{sectionIndex, itemIndex}] {
				kept = append(kept, item)
				continue
			}
			context.Truncations = append(context.Truncations, GuardianElasticTruncation{
				Component:     section.ID,
				OriginalBytes: len(item.Text),
			})
			context.Truncated = true
		}
		section.Items = kept
	}
}

func guardianElasticItemTokens(item GuardianElasticItem) int {
	if item.TokenCost > 0 {
		return item.TokenCost
	}
	if item.ImageURL != "" {
		return GuardianElasticImageTokenReservation
	}
	if item.Text == "" {
		return 0
	}
	return utils.ApproxTokenCount(item.Text)
}

func guardianElasticSectionTokens(section GuardianElasticSectionOutput) int {
	if section.Native {
		total := 0
		for _, item := range section.Items {
			total += guardianElasticItemTokens(item)
		}
		return total
	}
	total := guardianElasticContentFramingTokens(len(section.Items))
	for _, item := range section.Items {
		total += guardianElasticItemTokens(item)
	}
	return total
}

func guardianElasticContentFramingTokens(itemCount int) int {
	if itemCount <= 0 {
		return 0
	}
	return utils.ApproxTokenCount(`{"type":"message","role":"user","content":[]}`) + itemCount - 1
}
