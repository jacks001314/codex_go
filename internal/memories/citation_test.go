package memories

import (
	"reflect"
	"testing"
)

func TestParseCitationSupportsLegacyThreadIDs(t *testing.T) {
	citations := []string{
		"<memory_citation>\n<citation_entries>\nMEMORY.md:1-2|note=[x]\n</citation_entries>\n<thread_ids>\nthread-a\n../bad\nthread-b\n</thread_ids>\n</memory_citation>",
	}
	parsed := ParseCitation(citations)
	if parsed == nil {
		t.Fatalf("ParseCitation() = nil")
	}
	if got := ThreadIDsFromCitation(parsed); !reflect.DeepEqual(got, []string{"thread-a", "thread-b"}) {
		t.Fatalf("ThreadIDsFromCitation() = %#v", got)
	}
}

func TestParseCitationExtractsEntriesAndRolloutIDs(t *testing.T) {
	citations := []string{
		"<citation_entries>\nMEMORY.md:1-2|note=[summary]\nrollout_summaries/foo.md:10-12|note=[details]\n</citation_entries>\n<rollout_ids>\nthread-a\nthread-b\nthread-a\n</rollout_ids>",
	}
	parsed := ParseCitation(citations)
	if parsed == nil {
		t.Fatalf("ParseCitation() = nil")
	}
	gotEntries := make([][]any, 0, len(parsed.Entries))
	for _, entry := range parsed.Entries {
		gotEntries = append(gotEntries, []any{entry.Path, entry.LineStart, entry.LineEnd, entry.Note})
	}
	wantEntries := [][]any{
		{"MEMORY.md", int64(1), int64(2), "summary"},
		{"rollout_summaries/foo.md", int64(10), int64(12), "details"},
	}
	if !reflect.DeepEqual(gotEntries, wantEntries) {
		t.Fatalf("entries = %#v", gotEntries)
	}
	if !reflect.DeepEqual(parsed.RolloutIDs, []string{"thread-a", "thread-b"}) {
		t.Fatalf("RolloutIDs = %#v", parsed.RolloutIDs)
	}
}

func TestParseCitationReturnsNilForEmptyInput(t *testing.T) {
	if got := ParseCitation([]string{"no memory citation here"}); got != nil {
		t.Fatalf("ParseCitation() = %#v, want nil", got)
	}
}
