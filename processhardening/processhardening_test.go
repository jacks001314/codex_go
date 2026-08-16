//go:build unix

package processhardening

import (
	"reflect"
	"testing"
)

// TestEnvKeysWithPrefixHandlesNonUTF8Entries mirrors Rust's
// env_keys_with_prefix_handles_non_utf8_entries: prefix filtering operates on
// raw string bytes, so a key containing a non-UTF8 byte is matched like any
// other byte sequence.
func TestEnvKeysWithPrefixHandlesNonUTF8Entries(t *testing.T) {
	// RÖDBURK as raw bytes: 'R' 0xD6 'D' 'B' 'U' 'R' 'K'.
	nonUTF8 := "R\xd6DBURK"
	vars := []string{
		"LD_PRELOAD=/tmp/evil.so",
		"LD_LIBRARY_PATH=/tmp",
		"PATH=/usr/bin",
		nonUTF8 + "=value",
	}
	got := envKeysWithPrefix(vars, "LD_")
	want := []string{"LD_PRELOAD", "LD_LIBRARY_PATH"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envKeysWithPrefix(LD_) = %#v, want %#v", got, want)
	}

	// A key that merely contains the prefix later must not match.
	got = envKeysWithPrefix(vars, "PATH")
	if !reflect.DeepEqual(got, []string{"PATH"}) {
		t.Fatalf("envKeysWithPrefix(PATH) = %#v, want [PATH]", got)
	}
}

// TestEnvKeysWithPrefixEmptyAndMissingPins the no-match paths.
func TestEnvKeysWithPrefixEmptyAndMissing(t *testing.T) {
	if got := envKeysWithPrefix(nil, "LD_"); len(got) != 0 {
		t.Fatalf("empty vars = %#v, want none", got)
	}
	if got := envKeysWithPrefix([]string{"HOME=/u", "PATH=/bin"}, "LD_"); len(got) != 0 {
		t.Fatalf("no matches = %#v, want none", got)
	}
}
