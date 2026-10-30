package state

import (
	"errors"
	"fmt"
	"sort"

	"codex_go/utils"
)

// GuardianElasticImageTokenReservation mirrors Rust's IMAGE_TOKEN_RESERVATION:
// an opaque image is charged conservatively and is never split.
const GuardianElasticImageTokenReservation = 10_000

// GuardianElasticPriority names the optional retention priority that selects a
// group. Only Tool changes the owning transcript group.
const GuardianElasticPriorityTool = "tool"

// GuardianElasticRetention marks one item required or optional. Required and
// historical evidence is never removed by elastic allocation; historical items
// differ only in how preparation renders them, not in this budget step.
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
	Sections  []GuardianElasticSectionOutput
	Truncated bool
}

// GuardianElasticRequestBudget is the complete input allowance and the context
// already carried by the reviewer (Rust RequestBudget).
type GuardianElasticRequestBudget struct {
	MaxInputTokens        int
	ExistingContextTokens int
}

// ErrGuardianElasticEvidenceLimit reports required evidence that cannot fit the
// complete-request budget.
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

// EnforceGuardianElasticBudget mirrors Rust PreparedElasticContext::enforce_budget
// (#58388): reserve existing context, required evidence, history, and the
// omission notice before selecting whole optional entries, favoring recent
// entries and redistributing leftover capacity without reordering content.
func EnforceGuardianElasticBudget(
	context GuardianElasticContext,
	config GuardianElasticBudgetConfig,
	budget GuardianElasticRequestBudget,
	omissionNotice string,
) (GuardianElasticContext, error) {
	sections := make([]GuardianElasticSectionOutput, 0, len(context.Sections))
	for _, section := range context.Sections {
		if section.ID == "budget_omission" {
			continue
		}
		sections = append(sections, section)
	}
	context.Sections = sections
	remaining := budget.MaxInputTokens - budget.ExistingContextTokens
	if remaining < 0 {
		return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
	}
	demand, err := collectGuardianElasticDemand(context)
	if err != nil {
		return GuardianElasticContext{}, err
	}
	contextTokens := context.EstimatedTokens()
	if contextTokens <= remaining && !context.Truncated {
		return context, nil
	}
	notice := GuardianElasticSectionOutput{
		ID: "budget_omission",
		Items: []GuardianElasticItem{{
			Text:      omissionNotice,
			Retention: GuardianElasticRequired(),
		}},
	}
	noticeTokens := guardianElasticSectionTokens(notice)
	if contextTokens+noticeTokens <= remaining {
		context.Sections = append(context.Sections, notice)
		return context, nil
	}
	allowance := remaining - (demand.requiredTokens + noticeTokens)
	if allowance < 0 {
		return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
	}
	selected := allocateGuardianElasticItems(&demand, config, allowance)
	retainGuardianElasticSelection(&context, selected)
	context.Sections = append(context.Sections, notice)
	if context.EstimatedTokens() > remaining {
		return GuardianElasticContext{}, GuardianElasticBudgetError{Section: "request_budget"}
	}
	return context, nil
}

type guardianElasticCandidate struct {
	sectionIndex int
	itemIndex    int
	cost         int
}

type guardianElasticGroupDemand struct {
	pending   []guardianElasticCandidate
	totalCost int
	quota     int
	used      int
}

type guardianElasticDemand struct {
	requiredTokens int
	groups         map[GuardianElasticSection]*guardianElasticGroupDemand
	contentCounts  []int
	userContent    []bool
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
		groups:        map[GuardianElasticSection]*guardianElasticGroupDemand{},
		contentCounts: make([]int, len(context.Sections)),
		userContent:   make([]bool, len(context.Sections)),
	}
	for sectionIndex, section := range context.Sections {
		if section.Native {
			cost := guardianElasticSectionTokens(section)
			optional := false
			if len(section.Items) > 0 {
				optional = section.Items[0].Retention.Optional
			}
			if !optional {
				demand.requiredTokens += cost
				continue
			}
			group, err := guardianElasticAllocationSection(section.ID, section.Items[0].Retention)
			if err != nil {
				return guardianElasticDemand{}, err
			}
			appendGuardianElasticCandidate(&demand, group, sectionIndex, 0, cost, 0)
			continue
		}
		demand.userContent[sectionIndex] = true
		requiredCount := 0
		for _, item := range section.Items {
			if !item.Retention.Optional {
				requiredCount++
			}
		}
		demand.contentCounts[sectionIndex] = requiredCount
		demand.requiredTokens += guardianElasticContentFramingTokens(requiredCount)
		count := requiredCount
		for itemIndex, item := range section.Items {
			cost := guardianElasticItemTokens(item)
			if !item.Retention.Optional {
				demand.requiredTokens += cost
				continue
			}
			group, err := guardianElasticAllocationSection(section.ID, item.Retention)
			if err != nil {
				return guardianElasticDemand{}, err
			}
			framing := guardianElasticContentFramingTokens(count+1) - guardianElasticContentFramingTokens(count)
			count++
			appendGuardianElasticCandidate(&demand, group, sectionIndex, itemIndex, cost, framing)
		}
	}
	return demand, nil
}

func appendGuardianElasticCandidate(demand *guardianElasticDemand, group GuardianElasticSection, sectionIndex int, itemIndex int, cost int, framing int) {
	entry := demand.groups[group]
	if entry == nil {
		entry = &guardianElasticGroupDemand{}
		demand.groups[group] = entry
	}
	entry.totalCost += cost + framing
	entry.pending = append(entry.pending, guardianElasticCandidate{sectionIndex: sectionIndex, itemIndex: itemIndex, cost: cost})
}

// candidateCost charges the shared user-content framing delta for the next
// selected item in the candidate's section (Rust Candidate::cost). Native
// sections have no shared framing.
func (demand *guardianElasticDemand) candidateCost(candidate guardianElasticCandidate) int {
	cost := candidate.cost
	if !demand.userContent[candidate.sectionIndex] {
		return cost
	}
	count := demand.contentCounts[candidate.sectionIndex]
	return cost + guardianElasticContentFramingTokens(count+1) - guardianElasticContentFramingTokens(count)
}

// selectCandidate charges the framing-aware cost and records the retained item,
// so later candidates in the same section see the updated count.
func (demand *guardianElasticDemand) selectCandidate(candidate guardianElasticCandidate) int {
	cost := demand.candidateCost(candidate)
	if demand.userContent[candidate.sectionIndex] {
		demand.contentCounts[candidate.sectionIndex]++
	}
	return cost
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

func allocateGuardianElasticItems(demand *guardianElasticDemand, config GuardianElasticBudgetConfig, allowance int) map[[2]int]bool {
	groups := demand.groups
	order := make([]GuardianElasticSection, 0, len(groups))
	for group := range groups {
		order = append(order, group)
	}
	rank := make(map[GuardianElasticSection]int, len(guardianElasticSectionOrder))
	for index, section := range guardianElasticSectionOrder {
		rank[section] = index
	}
	sort.Slice(order, func(i int, j int) bool { return rank[order[i]] < rank[order[j]] })
	for _, group := range order {
		entry := groups[group]
		limits := config.SectionLimits(group)
		smallest := 0
		for _, candidate := range entry.pending {
			cost := demand.candidateCost(candidate)
			if smallest == 0 || cost < smallest {
				smallest = cost
			}
		}
		quota := limits.MinTotalSectionTokens
		if smallest > quota {
			quota = smallest
		}
		if entry.totalCost < quota {
			quota = entry.totalCost
		}
		entry.quota = quota
	}
	minimum := 0
	for _, entry := range groups {
		minimum += entry.quota
	}
	if minimum > allowance {
		for _, entry := range groups {
			entry.quota = int(int64(entry.quota) * int64(allowance) / int64(minimum))
		}
	}
	available := allowance
	for _, entry := range groups {
		available -= entry.quota
	}
	for {
		active := 0
		for _, entry := range groups {
			if entry.totalCost > entry.quota {
				active++
			}
		}
		if active == 0 || available/active == 0 {
			break
		}
		share := available / active
		for _, group := range order {
			entry := groups[group]
			granted := share
			if remaining := entry.totalCost - entry.quota; remaining < granted {
				granted = remaining
			}
			if granted < 0 {
				granted = 0
			}
			entry.quota += granted
			available -= granted
		}
	}
	selected := map[[2]int]bool{}
	remaining := allowance
	for _, group := range order {
		entry := groups[group]
		deferred := make([]guardianElasticCandidate, 0, len(entry.pending))
		for index := len(entry.pending) - 1; index >= 0; index-- {
			candidate := entry.pending[index]
			if demand.candidateCost(candidate) <= entry.quota-entry.used {
				cost := demand.selectCandidate(candidate)
				selected[[2]int{candidate.sectionIndex, candidate.itemIndex}] = true
				entry.used += cost
				remaining -= cost
				continue
			}
			deferred = append(deferred, candidate)
		}
		for left, right := 0, len(deferred)-1; left < right; left, right = left+1, right-1 {
			deferred[left], deferred[right] = deferred[right], deferred[left]
		}
		entry.pending = deferred
	}
	for remaining > 0 {
		var chosen *guardianElasticGroupDemand
		for _, group := range order {
			entry := groups[group]
			if len(entry.pending) == 0 {
				continue
			}
			if chosen == nil || entry.used < chosen.used {
				chosen = entry
			}
		}
		if chosen == nil {
			break
		}
		candidate := chosen.pending[len(chosen.pending)-1]
		chosen.pending = chosen.pending[:len(chosen.pending)-1]
		if demand.candidateCost(candidate) <= remaining {
			cost := demand.selectCandidate(candidate)
			selected[[2]int{candidate.sectionIndex, candidate.itemIndex}] = true
			chosen.used += cost
			remaining -= cost
		}
	}
	return selected
}

func retainGuardianElasticSelection(context *GuardianElasticContext, selected map[[2]int]bool) {
	for sectionIndex := range context.Sections {
		section := &context.Sections[sectionIndex]
		if section.Native {
			if len(section.Items) > 0 && section.Items[0].Retention.Optional && !selected[[2]int{sectionIndex, 0}] {
				section.Items = nil
				context.Truncated = true
			}
			continue
		}
		kept := make([]GuardianElasticItem, 0, len(section.Items))
		for itemIndex, item := range section.Items {
			if !item.Retention.Optional || selected[[2]int{sectionIndex, itemIndex}] {
				kept = append(kept, item)
				continue
			}
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
