package bottompane

import "unicode"
import "unicode/utf8"

// Rust parity: codex-rs/tui/src/bottom_pane/prompt_args.rs.

type PromptArgs struct {
	Text string
	Args []string
}

type SlashNameParse struct {
	Name       string
	Rest       string
	RestOffset int
	OK         bool
}

func ParseSlashName(line string) SlashNameParse {
	if line == "" || line[0] != '/' {
		return SlashNameParse{}
	}
	stripped := line[1:]
	nameEnd := len(stripped)
	for idx, r := range stripped {
		if unicode.IsSpace(r) {
			nameEnd = idx
			break
		}
	}
	name := stripped[:nameEnd]
	if name == "" {
		return SlashNameParse{}
	}
	restUntrimmed := stripped[nameEnd:]
	trimmedBytes := len(restUntrimmed)
	rest := restUntrimmed
	for len(rest) > 0 {
		r, size := utf8.DecodeRuneInString(rest)
		if !unicode.IsSpace(r) {
			break
		}
		rest = rest[size:]
	}
	restStartInStripped := nameEnd + (trimmedBytes - len(rest))
	return SlashNameParse{
		Name:       name,
		Rest:       rest,
		RestOffset: restStartInStripped + 1,
		OK:         true,
	}
}
