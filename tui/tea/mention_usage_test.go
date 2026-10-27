package tea

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFilterSkillPopupItemsPrefersFrequentSelectionsWithinTier(t *testing.T) {
	items := []skillPopupItem{
		{Name: "statsig", DisplayName: "statsig", Path: "skill://statsig"},
		{Name: "status", DisplayName: "status", Path: "skill://status"},
		{Name: "hello-statsig", DisplayName: "hello-statsig", Path: "skill://hello-statsig"},
	}
	frequencies := map[string]int{
		"skill://hello-statsig": 2,
		"skill://status":        1,
	}
	got := filterSkillPopupItems(items, "stat", frequencies)
	if len(got) != 3 {
		t.Fatalf("items = %#v, want three", got)
	}
	if got[0].Name != "status" || got[1].Name != "statsig" || got[2].Name != "hello-statsig" {
		t.Fatalf("order = %q, %q, %q", got[0].Name, got[1].Name, got[2].Name)
	}

	exact := filterSkillPopupItems(items, "statsig", frequencies)
	if len(exact) == 0 || exact[0].Name != "statsig" {
		t.Fatalf("exact match must win: %#v", exact)
	}
}

func TestMentionUsagePersistsBoundedWindow(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, mentionUsageFileName)
	seeded := make([]string, 0, mentionUsageHistoryLimit)
	for index := 0; index < mentionUsageHistoryLimit+5; index++ {
		if index < mentionUsageHistoryLimit {
			seeded = append(seeded, "skill://item-"+strconv.Itoa(index))
		}
	}
	data, err := json.Marshal(seeded)
	if err != nil {
		t.Fatalf("encode seed: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("seed mention usage: %v", err)
	}

	usage := newMentionUsage(home, "save-all")
	usage.record("skill://item-50")
	usage.flush()

	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mention usage: %v", err)
	}
	var recent []string
	if err := json.Unmarshal(data, &recent); err != nil {
		t.Fatalf("decode mention usage: %v", err)
	}
	if len(recent) != mentionUsageHistoryLimit {
		t.Fatalf("recent = %d, want %d", len(recent), mentionUsageHistoryLimit)
	}
	if recent[0] != "skill://item-1" || recent[len(recent)-1] != "skill://item-50" {
		t.Fatalf("window = %q..%q", recent[0], recent[len(recent)-1])
	}

	reloaded := newMentionUsage(home, "save-all")
	defer reloaded.flush()
	counts := reloaded.frequencies()
	if counts["skill://item-50"] != 1 || counts["skill://item-0"] != 0 {
		t.Fatalf("reloaded frequencies = %#v", counts)
	}

	// The in-memory window is bounded even when writes are not persisted.
	memory := newMentionUsage("", "none")
	for index := 0; index < mentionUsageHistoryLimit+5; index++ {
		memory.record("skill://memory-" + strconv.Itoa(index))
	}
	memoryCounts := memory.frequencies()
	if memoryCounts["skill://memory-54"] != 1 || memoryCounts["skill://memory-0"] != 0 {
		t.Fatalf("in-memory window = %#v", memoryCounts)
	}
}

func TestMentionUsageDisabledAndMalformedHistory(t *testing.T) {
	home := t.TempDir()
	disabled := newMentionUsage(home, "none")
	disabled.record("skill://alpha")
	disabled.flush()
	if _, err := os.Stat(filepath.Join(home, mentionUsageFileName)); !os.IsNotExist(err) {
		t.Fatalf("history-disabled usage wrote a file: %v", err)
	}
	if disabled.frequencies()["skill://alpha"] != 1 {
		t.Fatal("in-memory preference must survive with persistence disabled")
	}

	path := filepath.Join(home, mentionUsageFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed malformed history: %v", err)
	}
	malformed := newMentionUsage(home, "save-all")
	if len(malformed.frequencies()) != 0 {
		t.Fatalf("malformed history should be empty: %#v", malformed.frequencies())
	}
	malformed.record("skill://beta")
	malformed.flush()
	reloaded := newMentionUsage(home, "save-all")
	if reloaded.frequencies()["skill://beta"] != 1 {
		t.Fatalf("selection after malformed history was not persisted: %#v", reloaded.frequencies())
	}
}
