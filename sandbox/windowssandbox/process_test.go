package windowssandbox

import "testing"

func TestMakeEnvBlockSortsLikeRust(t *testing.T) {
	block := MakeEnvBlock(map[string]string{
		"path": "lower",
		"Path": "upper",
		"ZED":  "last",
		"abc":  "first",
	})
	got := utf16BlockStrings(block)
	// Windows keys are case-insensitive: "Path" sorts before "path", so the
	// first spelling wins and the duplicate is omitted (Rust #51512).
	want := []string{"abc=first", "Path=upper", "ZED=last"}
	if len(got) != len(want) {
		t.Fatalf("env block entries = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env block entries = %#v, want %#v", got, want)
		}
	}
	if len(block) == 0 || block[len(block)-1] != 0 {
		t.Fatalf("env block missing final NUL: %#v", block)
	}
}

// TestMakeEnvBlockDeduplicatesWindowsKeysLikeRust mirrors #51512: permission
// resolution and the child environment must select the same first spelling when
// callers supply duplicate case-insensitive TEMP/TMP keys.
func TestMakeEnvBlockDeduplicatesWindowsKeysLikeRust(t *testing.T) {
	block := MakeEnvBlock(map[string]string{
		"Temp": `C:\first`,
		"temp": `C:\second`,
		"TMP":  `C:\tmp`,
	})
	got := utf16BlockStrings(block)
	want := []string{`Temp=C:\first`, `TMP=C:\tmp`}
	if len(got) != len(want) {
		t.Fatalf("env block entries = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env block entries = %#v, want %#v", got, want)
		}
	}
}

func utf16BlockStrings(block []uint16) []string {
	var out []string
	start := 0
	for i, ch := range block {
		if ch != 0 {
			continue
		}
		if i == start {
			break
		}
		out = append(out, string(runesFromUTF16(block[start:i])))
		start = i + 1
	}
	return out
}

func runesFromUTF16(values []uint16) []rune {
	out := make([]rune, 0, len(values))
	for _, value := range values {
		out = append(out, rune(value))
	}
	return out
}
