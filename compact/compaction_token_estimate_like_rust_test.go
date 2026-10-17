package compact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncryptedCompactionUsesReplayCacheTokenEstimateLikeRust(t *testing.T) {
	for _, tc := range []struct {
		encodedLen int
		want       int
	}{
		{encodedLen: 0, want: 0},
		{encodedLen: 1, want: 1},
		{encodedLen: 11, want: 4},
		{encodedLen: 12, want: 4},
		{encodedLen: 13, want: 5},
		{encodedLen: 8_000, want: 2_667},
	} {
		if got := EstimateCompactionContentLength(tc.encodedLen); got != tc.want {
			t.Fatalf("EstimateCompactionContentLength(%d) = %d, want %d", tc.encodedLen, got, tc.want)
		}
	}

	for _, itemType := range []string{"compaction", "context_compaction"} {
		raw, err := json.Marshal(map[string]any{
			"type":              itemType,
			"id":                "cmp_test",
			"encrypted_content": strings.Repeat("a", 8_000),
		})
		if err != nil {
			t.Fatal(err)
		}
		item := Item{Type: itemType, Raw: raw}
		wireBytes, ok := CompactionItemWireBytes(&item)
		if !ok || wireBytes != len(raw) {
			t.Fatalf("%s wire bytes = %d/%v, want %d", itemType, wireBytes, ok, len(raw))
		}
		if got := EstimateItemTokens(&item); got <= 667 {
			t.Fatalf("%s token estimate = %d, want framing plus %d content tokens", itemType, got, 667)
		}
	}
}
