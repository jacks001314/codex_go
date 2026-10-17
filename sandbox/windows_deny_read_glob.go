package sandbox

import "strings"

// WindowsDenyReadGlobScan is the lexical scan bound shared by policy validation
// and native Windows ACL expansion for a deny-read glob (Rust #43903). Root is
// the deepest literal directory before the first glob metacharacter,
// PatternSuffix is the glob relative to that root, and MaxDepth bounds the
// traversal.
type WindowsDenyReadGlobScan struct {
	Root          string
	PatternSuffix string
	MaxDepth      *int
}

// WindowsDenyReadGlobScanForPattern plans a Windows deny-read glob using only
// string inspection, without touching the filesystem. A recursive `**`
// component inherits the configured depth bound, while a non-recursive suffix
// is capped at its component count (Rust #43903).
func WindowsDenyReadGlobScanForPattern(pattern string, configuredMaxDepth *int) WindowsDenyReadGlobScan {
	firstGlob := len(pattern)
	for index, ch := range pattern {
		if ch == '*' || ch == '?' || ch == '[' {
			firstGlob = index
			break
		}
	}
	literalPrefix := pattern[:firstGlob]
	separatorIndex := strings.LastIndexAny(literalPrefix, `/\`)
	if separatorIndex < 0 {
		return WindowsDenyReadGlobScan{
			Root:          ".",
			PatternSuffix: pattern,
			MaxDepth:      windowsDenyReadGlobScanMaxDepth(pattern, configuredMaxDepth),
		}
	}
	patternSuffix := pattern[separatorIndex+1:]
	end := separatorIndex
	if separatorIndex == 0 || (separatorIndex > 0 && literalPrefix[separatorIndex-1] == ':') {
		// Keep the leading separator (or drive root) as part of the scan root.
		end = separatorIndex + 1
	}
	return WindowsDenyReadGlobScan{
		Root:          literalPrefix[:end],
		PatternSuffix: patternSuffix,
		MaxDepth:      windowsDenyReadGlobScanMaxDepth(patternSuffix, configuredMaxDepth),
	}
}

func windowsDenyReadGlobScanMaxDepth(patternSuffix string, configuredMaxDepth *int) *int {
	components := windowsDenyReadGlobComponents(patternSuffix)
	for _, component := range components {
		if component == "**" {
			if configuredMaxDepth == nil {
				return nil
			}
			value := *configuredMaxDepth
			return &value
		}
	}
	value := len(components)
	if configuredMaxDepth != nil && *configuredMaxDepth < value {
		value = *configuredMaxDepth
	}
	return &value
}

func windowsDenyReadGlobComponents(pattern string) []string {
	raw := strings.FieldsFunc(pattern, func(r rune) bool { return r == '/' || r == '\\' })
	out := raw[:0]
	for _, component := range raw {
		if component != "" {
			out = append(out, component)
		}
	}
	return out
}
