package appserver

// Rust parity: codex-rs/core/src/agent/helper_daybreak.rs and
// helper_daybreak/turn.rs (#60749, #60773). A native helper inherits the
// spawning turn's saved Daybreak choice, and its own saved preference takes
// precedence on later turns.

import (
	"errors"
	"fmt"
	"strings"

	"codex_go/auth"
	"codex_go/features"
	modelpkg "codex_go/model"
	"codex_go/session"
)

const helperDaybreakAccountChanged = "The account for this subagent's saved Daybreak choice has changed. Start a new subagent."

// helperDaybreakIntent is the choice and account captured when a helper was
// spawned, plus the program the parent turn had already resolved.
type helperDaybreakIntent struct {
	choice    modelpkg.SavedDaybreakChoice
	accountID string
	inherited string
	recorded  bool
}

// recordHelperDaybreakIntent keeps the spawn-time intent for one helper thread.
func (r *RuntimeRouter) recordHelperDaybreakIntent(threadID string, intent helperDaybreakIntent) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return
	}
	r.helperDaybreakMu.Lock()
	defer r.helperDaybreakMu.Unlock()
	if r.helperDaybreak == nil {
		r.helperDaybreak = map[string]helperDaybreakIntent{}
	}
	intent.recorded = true
	r.helperDaybreak[threadID] = intent
}

// helperDaybreakIntentFor returns the intent recorded at spawn, if any.
func (r *RuntimeRouter) helperDaybreakIntentFor(threadID string) (helperDaybreakIntent, bool) {
	if r == nil {
		return helperDaybreakIntent{}, false
	}
	r.helperDaybreakMu.Lock()
	defer r.helperDaybreakMu.Unlock()
	intent, ok := r.helperDaybreak[strings.TrimSpace(threadID)]
	return intent, ok
}

// helperDaybreakPropagationEnabled mirrors the disabled-by-default
// `daybreak_program_propagation` feature resolved from the parent turn.
func (c *runtimeAgentController) helperDaybreakPropagationEnabled() bool {
	if c == nil || c.router == nil {
		return false
	}
	active := c.router.threads.ActiveTurn(c.parentID)
	if active == nil || active.Params == nil {
		return false
	}
	cfg, err := c.router.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return false
	}
	return features.Enabled(cfg.FeatureSettings(), "daybreak_program_propagation")
}

// helperDaybreakOwner reports the ChatGPT account that may own a saved Daybreak
// choice, mirroring Rust helper_daybreak::eligible_owner.
func (r *RuntimeRouter) helperDaybreakOwner() (string, bool) {
	if r == nil {
		return "", false
	}
	snapshot := r.currentCachedAuthSnapshot()
	account := auth.AccountFromAuth(snapshot)
	if account == nil || account.Type != auth.AccountChatGPT {
		return "", false
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
	if accountID == "" {
		return "", false
	}
	return accountID, true
}

// helperDaybreakSelectProgram mirrors Rust select_program: a saved choice only
// selects an advertised program, and an incompatible choice is an error rather
// than a silent downgrade.
func helperDaybreakSelectProgram(info *modelpkg.ModelInfo, choice modelpkg.SavedDaybreakChoice) (string, error) {
	if info == nil || info.AvailableAccessPrograms == nil {
		return "", nil
	}
	programs := info.AvailableAccessPrograms
	if selected := programs.SelectSaved(choice); selected != nil {
		return string(*selected), nil
	}
	switch choice {
	case modelpkg.SavedDaybreakChoiceOn:
		if len(programs.Cyber) > 0 {
			return "", fmt.Errorf("Model %s does not advertise Daybreak support. Turn Daybreak off or choose a compatible model.", helperDaybreakModelSlug(info))
		}
	case modelpkg.SavedDaybreakChoiceOff:
		return "", fmt.Errorf("Model %s does not advertise Standard. Choose a model that supports turning Daybreak off.", helperDaybreakModelSlug(info))
	}
	return "", nil
}

// helperDaybreakAdaptInherited keeps an inherited program when the child model
// advertises it and otherwise adapts a Daybreak Blue/Red choice to the child's
// available Daybreak program (Blue before Red, Rust #60773).
func helperDaybreakAdaptInherited(info *modelpkg.ModelInfo, program string) string {
	program = strings.TrimSpace(program)
	if program == "" || info == nil || info.AvailableAccessPrograms == nil {
		return program
	}
	programs := info.AvailableAccessPrograms.Cyber
	advertised := func(candidate modelpkg.CyberAccessProgram) bool {
		for _, value := range programs {
			if value == candidate {
				return true
			}
		}
		return false
	}
	switch modelpkg.CyberAccessProgram(program) {
	case modelpkg.CyberAccessProgramDaybreakBlue, modelpkg.CyberAccessProgramDaybreakRed:
		if advertised(modelpkg.CyberAccessProgram(program)) {
			return program
		}
		for _, candidate := range []modelpkg.CyberAccessProgram{
			modelpkg.CyberAccessProgramDaybreakBlue,
			modelpkg.CyberAccessProgramDaybreakRed,
		} {
			if advertised(candidate) {
				return string(candidate)
			}
		}
		// The child model cannot use Daybreak, so the inherited program is dropped.
		return ""
	default:
		return program
	}
}

// resolveHelperDaybreakProgram resolves the program a helper turn should send.
// The helper's own saved preference wins; otherwise the choice inherited at
// spawn applies, and an implicit program is adapted to the child's model.
func (c *runtimeAgentController) resolveHelperDaybreakProgram(threadID string, record *session.Record) (string, error) {
	inherited := c.cyberAccessProgram
	if !c.helperDaybreakPropagationEnabled() {
		return inherited, nil
	}
	intent, hasIntent := c.router.helperDaybreakIntentFor(threadID)
	if hasIntent && intent.inherited != "" {
		inherited = intent.inherited
	}
	providerID := ""
	modelID := ""
	var saved *bool
	if record != nil {
		providerID = strings.TrimSpace(record.Metadata.ModelProvider)
		modelID = strings.TrimSpace(record.Metadata.Model)
		saved = record.Metadata.DaybreakEnabled
	}
	if providerID != "" && providerID != modelpkg.OpenAIProviderID {
		return inherited, nil
	}
	info := c.router.modelInfoForRuntime(modelID)
	if saved != nil {
		return helperDaybreakSelectProgram(info, modelpkg.SavedDaybreakChoiceFrom(saved))
	}
	if hasIntent && intent.choice != modelpkg.SavedDaybreakChoiceUnset {
		accountID, eligible := c.router.helperDaybreakOwner()
		if !eligible || accountID != intent.accountID {
			return "", errors.New(helperDaybreakAccountChanged)
		}
		return helperDaybreakSelectProgram(info, intent.choice)
	}
	return helperDaybreakAdaptInherited(info, inherited), nil
}

// recordSpawnHelperDaybreakIntent captures the parent's saved choice for a new
// native helper. A failed metadata read is returned when the parent had no
// explicit program, so the child is not silently started without its choice.
func (c *runtimeAgentController) recordSpawnHelperDaybreakIntent(threadID string) error {
	if !c.helperDaybreakPropagationEnabled() {
		return nil
	}
	intent := helperDaybreakIntent{inherited: c.cyberAccessProgram}
	intent.accountID, _ = c.router.helperDaybreakOwner()
	if parent, err := c.router.threadRecord(session.ThreadID(c.parentID), false, false); err == nil && parent != nil {
		if parent.Metadata.DaybreakEnabled != nil {
			intent.choice = modelpkg.SavedDaybreakChoiceFrom(parent.Metadata.DaybreakEnabled)
		}
	} else if err != nil && strings.TrimSpace(c.cyberAccessProgram) == "" {
		return fmt.Errorf("Could not read the Daybreak preference for thread %s: %v", c.parentID, err)
	}
	c.router.recordHelperDaybreakIntent(threadID, intent)
	return nil
}

func helperDaybreakModelSlug(info *modelpkg.ModelInfo) string {
	if info == nil {
		return ""
	}
	return strings.TrimSpace(info.Slug)
}
