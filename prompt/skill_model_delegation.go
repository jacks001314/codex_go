package prompt

import "strings"

// Rust parity: codex-rs/skills/src/model_delegation.rs (#38467/#38475).

const (
	lunaModel  = "gpt-5.6-luna"
	terraModel = "gpt-5.6-terra"
	solModel   = "gpt-5.6-sol"

	maxTargetModelBytes           = 128
	maxDelegationInstructionBytes = 2048
)

// SkillModel is the model requested for work governed by a skill.
type SkillModel string

const (
	SkillModelLuna SkillModel = "luna"
)

// SkillModelDelegationInstruction holds bounded instructions for delegating
// skill-governed work to a cheaper model (Rust SkillModelDelegationInstruction).
type SkillModelDelegationInstruction struct {
	text string
}

// BuildSkillModelDelegationInstruction mirrors Rust
// SkillModelDelegationInstruction::from_skill_model: it returns bounded
// instructions only when the skill requests an available lower-tier model and
// the current model is a provider-prefixed Sol or Terra model.
func BuildSkillModelDelegationInstruction(skillModel SkillModel, skillName string, currentModel string, availableModels []string) *SkillModelDelegationInstruction {
	if strings.ContainsAny(skillName, "`<>") {
		return nil
	}
	providerPrefix := ""
	found := false
	for _, parentModel := range []string{solModel, terraModel} {
		prefix, ok := strings.CutSuffix(currentModel, parentModel)
		if !ok {
			continue
		}
		if prefix == "" || strings.HasSuffix(prefix, ".") || strings.HasSuffix(prefix, "/") {
			providerPrefix = prefix
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	targetModel := providerPrefix + lunaModel
	targetFound := false
	for _, model := range availableModels {
		if model == targetModel && isSafeModelIdentifier(model) {
			targetFound = true
			break
		}
	}
	if !targetFound {
		return nil
	}
	skillModelLabel := string(skillModel)
	if skillModelLabel == "" {
		skillModelLabel = string(SkillModelLuna)
	}
	instruction := "<skill_model_delegation>\n" +
		"For this invocation only, skill `" + skillName + "` requests `model: " + skillModelLabel + "`. " +
		"If the user prohibits delegation or subagents, or the work depends on an image or audio " +
		"attachment, work locally. Otherwise, delegate only self-contained text-based skill work " +
		"and use `spawn_agent` " +
		"exactly once. Set `model` to `" + targetModel + "`, set `fork_turns` to `\"none\"`, and choose a " +
		"unique `task_name`. Omit `agent_type` and `reasoning_effort`.\n" +
		"Give the child only this skill's work and necessary context; never include this block and tell " +
		"it not to spawn agents. Delegate the whole request only if the skill fully covers it; otherwise, " +
		"retain ownership of the remaining work and final answer. Wait for the result. If spawning " +
		"fails, continue locally. If waiting fails, retry waiting without duplicating the " +
		"child's work.\n" +
		"Ignore this instruction in child agents and on later turns.\n" +
		"</skill_model_delegation>"
	if len(instruction) > maxDelegationInstructionBytes {
		return nil
	}
	return &SkillModelDelegationInstruction{text: instruction}
}

// Text returns the bounded model-visible delegation instruction.
func (i *SkillModelDelegationInstruction) Text() string {
	if i == nil {
		return ""
	}
	return i.text
}

func isSafeModelIdentifier(model string) bool {
	if len(model) > maxTargetModelBytes {
		return false
	}
	for _, character := range model {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '.', '/', '_', '-':
			continue
		default:
			return false
		}
	}
	return true
}
