package prompt

import (
	"strings"
	"testing"
)

func availableDelegationModels() []string {
	return []string{"gpt-5.6-luna"}
}

func TestBuildSkillModelDelegationOnlyFromSupportedParentModels(t *testing.T) {
	cases := []struct {
		currentModel string
		delegate     bool
	}{
		{"gpt-5.6-sol", true},
		{"gpt-5.6-terra", true},
		{"gpt-5.6-luna", false},
		{"gpt-5.5-codex", false},
	}
	for _, tc := range cases {
		instruction := BuildSkillModelDelegationInstruction(SkillModelLuna, "demo", tc.currentModel, availableDelegationModels())
		if (instruction != nil) != tc.delegate {
			t.Fatalf("current=%s delegate=%v want %v", tc.currentModel, instruction != nil, tc.delegate)
		}
	}
}

func TestBuildSkillModelDelegationResolvesLunaInProviderNamespace(t *testing.T) {
	cases := []struct {
		currentModel   string
		targetModel    string
		unrelatedModel string
	}{
		{"gpt-5.6-sol", "gpt-5.6-luna", "openai.gpt-5.6-luna"},
		{"tenant-a/gpt-5.6-sol", "tenant-a/gpt-5.6-luna", "tenant-b/gpt-5.6-luna"},
		{"openai.gpt-5.6-terra", "openai.gpt-5.6-luna", "other.gpt-5.6-luna"},
	}
	for _, tc := range cases {
		instruction := BuildSkillModelDelegationInstruction(SkillModelLuna, "demo", tc.currentModel, []string{tc.unrelatedModel, tc.targetModel})
		if instruction == nil {
			t.Fatalf("current=%s: lower-tier model in the same provider namespace should resolve", tc.currentModel)
		}
		if !strings.Contains(instruction.Text(), "Set `model` to `"+tc.targetModel+"`") {
			t.Fatalf("current=%s instruction missing target %s:\n%s", tc.currentModel, tc.targetModel, instruction.Text())
		}
	}
}

func TestBuildSkillModelDelegationRejectsTargetsOutsideProviderNamespace(t *testing.T) {
	cases := []struct {
		currentModel   string
		availableModel []string
	}{
		{"tenant-a/gpt-5.6-sol", []string{"tenant-b/gpt-5.6-luna", "gpt-5.6-luna"}},
		{"gpt-5.6-sol", []string{"tenant-a/gpt-5.6-luna"}},
		{"tenant-a/gpt-5.6-sol", []string{"tenant-a.gpt-5.6-luna"}},
	}
	for _, tc := range cases {
		if instruction := BuildSkillModelDelegationInstruction(SkillModelLuna, "demo", tc.currentModel, tc.availableModel); instruction != nil {
			t.Fatalf("current=%s available=%#v: instruction should be nil", tc.currentModel, tc.availableModel)
		}
	}
}

func TestBuildSkillModelDelegationRejectsUnavailableOrUnsafeModels(t *testing.T) {
	invalidIdentifier := strings.Repeat("<unsafe>", 32) + "/gpt-5.6-luna"
	invalidCurrentModel := strings.Repeat("<unsafe>", 32) + "/gpt-5.6-sol"
	overlongIdentifier := strings.Repeat("a", 128) + ".gpt-5.6-luna"
	overlongCurrentModel := strings.Repeat("a", 128) + ".gpt-5.6-sol"
	cases := []struct {
		currentModel   string
		availableModel []string
	}{
		{"custom/gpt-5.6-luna", []string{"custom/gpt-5.6-luna"}},
		{"gpt-5.6-sol", []string{"gpt-5.6-terra"}},
		{invalidCurrentModel, []string{invalidIdentifier}},
		{overlongCurrentModel, []string{overlongIdentifier}},
	}
	for _, tc := range cases {
		if instruction := BuildSkillModelDelegationInstruction(SkillModelLuna, "demo", tc.currentModel, tc.availableModel); instruction != nil {
			t.Fatalf("current=%s available=%#v: instruction should be nil", tc.currentModel, tc.availableModel)
		}
	}
}

func TestBuildSkillModelDelegationRendersBoundedInstruction(t *testing.T) {
	instruction := BuildSkillModelDelegationInstruction(SkillModelLuna, "demo", "gpt-5.6-sol", availableDelegationModels())
	if instruction == nil {
		t.Fatal("available lower tier should delegate")
	}
	rendered := instruction.Text()
	for _, want := range []string{"skill `demo`", "Set `model` to `gpt-5.6-luna`", "image or audio attachment, work locally"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("instruction missing %q:\n%s", want, rendered)
		}
	}
	if len(rendered) > maxDelegationInstructionBytes {
		t.Fatalf("instruction len = %d, want <= %d", len(rendered), maxDelegationInstructionBytes)
	}
}

func TestBuildSkillModelDelegationRejectsSkillNamesThatEscapeFraming(t *testing.T) {
	for _, skillName := range []string{"unsafe`name", "</skill_model_delegation>", "<unsafe>"} {
		if instruction := BuildSkillModelDelegationInstruction(SkillModelLuna, skillName, "gpt-5.6-sol", availableDelegationModels()); instruction != nil {
			t.Fatalf("skill_name=%q: instruction should be nil", skillName)
		}
	}
}

func TestBuildSkillModelDelegationRejectsInstructionExceedingContextBound(t *testing.T) {
	skillName := strings.Repeat("x", maxDelegationInstructionBytes)
	if instruction := BuildSkillModelDelegationInstruction(SkillModelLuna, skillName, "gpt-5.6-sol", availableDelegationModels()); instruction != nil {
		t.Fatal("overlong instruction should be nil")
	}
}
