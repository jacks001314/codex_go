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
	want := []string{"abc=first", "Path=upper", "path=lower", "ZED=last"}
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
