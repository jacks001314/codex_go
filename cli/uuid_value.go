package cli

import (
	"fmt"
	"strings"
)

// uuidValueDiagnostic reports a `THREAD_ID` value the way uuid 1.x's
// `Uuid::parse_str` does. It returns ("", true) for the shapes the crate
// accepts (simple 32, hyphenated 36, braced 38 and `urn:uuid:` 45) and
// uuid's detailed `InvalidUuid::into_err` message otherwise.
func uuidValueDiagnostic(value string) (string, bool) {
	if uuidAccepts(value) {
		return "", true
	}
	return uuidParseError(value), false
}

func uuidAccepts(value string) bool {
	switch len(value) {
	case 32:
		return uuidSimpleFormatOK(value)
	case 36:
		return uuidHyphenatedOK(value)
	case 38:
		return value[0] == '{' && value[len(value)-1] == '}' && uuidHyphenatedOK(value[1:len(value)-1])
	case 45:
		return strings.HasPrefix(value, uuidURNPrefix) && uuidHyphenatedOK(value[len(uuidURNPrefix):])
	}
	return false
}

const uuidURNPrefix = "urn:uuid:"

// uuidHyphenatedOK mirrors `parse_hyphenated`: the four hyphens sit at 8, 13,
// 18 and 23 and every other position is hexadecimal.
func uuidHyphenatedOK(value string) bool {
	if len(value) != 36 {
		return false
	}
	if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for _, start := range [...]int{0, 4, 9, 14, 19, 24, 28, 32} {
		for _, offset := range [...]int{0, 1, 2, 3} {
			if !uuidHexDigit(value[start+offset]) {
				return false
			}
		}
	}
	return true
}

// uuidSimpleFormatOK mirrors `parse_simple`: 32 hexadecimal digits.
func uuidSimpleFormatOK(value string) bool {
	if len(value) != 32 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !uuidHexDigit(value[i]) {
			return false
		}
	}
	return true
}

func uuidHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

// uuidParseError mirrors `InvalidUuid::into_err`. The prefix or the braces are
// stripped for the character scan but the reported indexes and the last group
// length stay relative to the original value, exactly like the crate.
func uuidParseError(value string) string {
	inner := value
	offset := 0
	simple := true
	if len(value) >= 2 && value[0] == '{' && value[len(value)-1] == '}' {
		inner = value[1 : len(value)-1]
		offset = 1
		simple = false
	} else if strings.HasPrefix(value, uuidURNPrefix) {
		inner = value[len(uuidURNPrefix):]
		offset = len(uuidURNPrefix)
		simple = false
	}
	const invalidCharacter = "invalid character: expected an optional prefix of `urn:uuid:` followed by [0-9a-fA-F-], found `%c` at %d"

	hyphenCount := 0
	var groupBounds [4]int
	for index, character := range inner {
		b := byte(character)
		if uint32(character)-uint32(b) > 0 {
			return fmt.Sprintf(invalidCharacter, character, index+offset+1)
		}
		switch {
		case b == '-':
			if hyphenCount < len(groupBounds) {
				groupBounds[hyphenCount] = index
			}
			hyphenCount++
		case !uuidHexDigit(b):
			return fmt.Sprintf(invalidCharacter, character, index+offset+1)
		}
	}
	if hyphenCount == 0 && simple {
		return fmt.Sprintf("invalid length: expected length 32 for simple format, found %d", len(value))
	}
	if hyphenCount != len(groupBounds) {
		return fmt.Sprintf("invalid group count: expected 5, found %d", hyphenCount+1)
	}
	blockStarts := [...]int{0, 9, 14, 19, 24}
	expected := [...]int{8, 4, 4, 4, 12}
	for group := 0; group < len(groupBounds); group++ {
		if groupBounds[group] != blockStarts[group+1]-1 {
			return fmt.Sprintf("invalid group length in group %d: expected %d, found %d",
				group, expected[group], groupBounds[group]-blockStarts[group])
		}
	}
	return fmt.Sprintf("invalid group length in group 4: expected %d, found %d", expected[4], len(value)-blockStarts[4])
}
