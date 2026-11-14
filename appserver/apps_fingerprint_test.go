package appserver

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

func TestAppsFingerprintMatchesRustJSONUnicode(t *testing.T) {
	for _, tc := range []struct{ text, serialized string }{
		{"<>&", "\"<>&\""},
		{"\u2028", "\"\u2028\""},
		{"\u2029", "\"\u2029\""},
		{`\u2028`, `"\\u2028"`},
		{`\u2029`, `"\\u2029"`},
		{"backslash\\\u2028", "\"backslash\\\\\u2028\""},
	} {
		sum := sha1.Sum([]byte(tc.serialized))
		want := hex.EncodeToString(sum[:])
		if got := appsInstructionsHash(tc.text); got != want {
			t.Errorf("text %q: hash=%s want=%s", tc.text, got, want)
		}
	}
}
