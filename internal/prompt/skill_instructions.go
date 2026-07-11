package prompt

import (
	"fmt"
	"unicode/utf8"
)

const (
	SkillMainPromptMaxBytes = 8000
	SkillNameMaxBytes       = 256
	SkillPathMaxBytes       = 1024
)

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
