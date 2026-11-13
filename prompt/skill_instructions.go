package prompt

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	SkillMainPromptMaxBytes = 8000
	SkillNameMaxBytes       = 256
	SkillPathMaxBytes       = 1024
)

// SkillPromptContentsBounded mirrors Rust's skill injection paths. Host roots
// are bounded only when the skill came from an agent-plugin root
// (ext/skills/src/host_prompt.rs, discovery mode DirectChildren); catalog
// entries from every other authority (executor, orchestrator, cloud, custom)
// are always bounded (ext/skills/src/extension.rs).
func SkillPromptContentsBounded(skill InstructionsSkillMetadata) bool {
	if skill.AgentPlugin {
		return true
	}
	kind := strings.ToLower(strings.TrimSpace(skill.AuthorityKind))
	return kind != "" && kind != "host"
}

func TruncateSkillInstructionFields(name string, path string, contents string) (string, string, string, bool) {
	contents, contentsTruncated := TruncateSkillUTF8Bytes(contents, SkillMainPromptMaxBytes)
	name, _ = TruncateSkillUTF8Bytes(name, SkillNameMaxBytes)
	path, _ = TruncateSkillUTF8Bytes(path, SkillPathMaxBytes)
	return name, path, contents, contentsTruncated
}

func TruncateSkillUTF8Bytes(value string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(value) <= maxBytes {
		return value, false
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end], true
}

func SkillMainPromptTruncatedWarning(name string) string {
	return fmt.Sprintf("Skill `%s` exceeded the main prompt context limit and was truncated.", name)
}
