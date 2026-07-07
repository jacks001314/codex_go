package utils

import "strings"

func ANSIExpandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	return strings.ReplaceAll(s, "\t", "    ")
}

func StripANSI(s string) string {
	s = ANSIExpandTabs(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			continue
		}
		switch s[i+1] {
		case '[':
			i += 2
			for i < len(s) && !isANSIFinalByte(s[i]) {
				i++
			}
		case ']':
			i += 2
			for i < len(s) {
				if s[i] == 0x07 {
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
				i++
			}
		default:
			i++
		}
	}
	return b.String()
}

func ANSIFirstLine(s string) string {
	text := StripANSI(s)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return strings.TrimSuffix(text[:idx], "\r")
	}
	return text
}

func isANSIFinalByte(ch byte) bool {
	return ch >= 0x40 && ch <= 0x7e
}
