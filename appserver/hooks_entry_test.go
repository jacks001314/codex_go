package appserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHookListEntryMarshalDefaultsEmptyCollections(t *testing.T) {
	raw, err := json.Marshal(&HookListEntry{CWD: "/repo"})
	if err != nil {
		t.Fatalf("Marshal HookListEntry error = %v", err)
	}
	if !strings.Contains(string(raw), `"hooks":[]`) || !strings.Contains(string(raw), `"warnings":[]`) || !strings.Contains(string(raw), `"errors":[]`) {
		t.Fatalf("HookListEntry JSON = %s", raw)
	}
	if strings.Contains(string(raw), "requiredLoadErrors") {
		t.Fatalf("HookListEntry JSON should omit empty requiredLoadErrors: %s", raw)
	}
}

func TestHookListEntryMarshalRequiredLoadErrors(t *testing.T) {
	raw, err := json.Marshal(&HookListEntry{CWD: "/repo", RequiredLoadErrors: []string{"failed to load required managed hook"}})
	if err != nil {
		t.Fatalf("Marshal HookListEntry error = %v", err)
	}
	if !strings.Contains(string(raw), `"requiredLoadErrors":["failed to load required managed hook"]`) {
		t.Fatalf("HookListEntry JSON = %s", raw)
	}
}
