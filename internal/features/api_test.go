package features

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestListPaginatesFeatures(t *testing.T) {
	service := NewFeatureService([]FeatureEntry{
		{Key: "b", Stage: FeatureStageBeta},
		{Key: "a", Stage: FeatureStageStable},
	})
	limit := 1
	first, err := service.List(&FeatureListParams{Limit: &limit})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(first.Data) != 1 || first.Data[0].Key != "a" || first.NextCursor == nil {
		t.Fatalf("unexpected first page: %#v", first)
	}
	second, err := service.List(&FeatureListParams{Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("List(second) error = %v", err)
	}
	if len(second.Data) != 1 || second.Data[0].Key != "b" {
		t.Fatalf("unexpected second page: %#v", second)
	}
}

func TestSetEnablementIgnoresUnknownKeys(t *testing.T) {
	service := NewFeatureService([]FeatureEntry{{Key: "known", Stage: FeatureStageBeta}})
	setLegacy, err := service.SetEnablement(&FeatureEnablementSetParams{Enabled: []string{"known", "missing"}})
	if err != nil {
		t.Fatalf("SetEnablement() error = %v", err)
	}
	if len(setLegacy.Enablement) != 1 || !setLegacy.Enablement["known"] {
		t.Fatalf("SetEnablement() response = %#v, want only known enabled", setLegacy.Enablement)
	}
	response, err := service.List(&FeatureListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !response.Data[0].Enabled {
		t.Fatalf("expected known feature enabled")
	}
	setMap, err := service.SetEnablement(&FeatureEnablementSetParams{Enablement: map[string]bool{"known": false, "missing": true}})
	if err != nil {
		t.Fatalf("SetEnablement(map) error = %v", err)
	}
	if len(setMap.Enablement) != 1 || setMap.Enablement["known"] {
		t.Fatalf("SetEnablement(map) response = %#v, want only known disabled", setMap.Enablement)
	}
	response, err = service.List(&FeatureListParams{})
	if err != nil {
		t.Fatalf("List(after map) error = %v", err)
	}
	if response.Data[0].Enabled {
		t.Fatalf("expected known feature disabled")
	}
	empty, err := service.SetEnablement(&FeatureEnablementSetParams{Enablement: map[string]bool{}})
	if err != nil {
		t.Fatalf("SetEnablement(empty) error = %v", err)
	}
	if len(empty.Enablement) != 0 {
		t.Fatalf("SetEnablement(empty) response = %#v, want empty enablement", empty.Enablement)
	}
}

func TestListRejectsInvalidCursor(t *testing.T) {
	service := NewFeatureService([]FeatureEntry{{Key: "known", Stage: FeatureStageBeta}})
	cursor := "bad"
	_, err := service.List(&FeatureListParams{Cursor: &cursor})
	if !errors.Is(err, ErrInvalidFeatureRequest) || !strings.Contains(err.Error(), "invalid cursor: bad") {
		t.Fatalf("List() error = %v, want invalid cursor sentinel", err)
	}
}

func TestFeatureWireShapeMatchesRust(t *testing.T) {
	entryPayload := marshalObjectForTest(t, &FeatureEntry{Key: "stable_feature", Stage: FeatureStageStable})
	if _, ok := entryPayload["key"]; ok {
		t.Fatalf("legacy key should not be emitted: %#v", entryPayload)
	}
	for _, nullableKey := range []string{"displayName", "description", "announcement"} {
		if value, ok := entryPayload[nullableKey]; !ok || value != nil {
			t.Fatalf("feature nullable key %q = %#v in %#v", nullableKey, value, entryPayload)
		}
	}
	if entryPayload["name"] != "stable_feature" || entryPayload["stage"] != string(FeatureStageStable) {
		t.Fatalf("feature payload = %#v", entryPayload)
	}

	paramsPayload := marshalObjectForTest(t, &FeatureListParams{})
	for _, nullableKey := range []string{"cursor", "limit", "threadId"} {
		if value, ok := paramsPayload[nullableKey]; !ok || value != nil {
			t.Fatalf("feature list nullable key %q = %#v in %#v", nullableKey, value, paramsPayload)
		}
	}

	enablementPayload := marshalObjectForTest(t, &FeatureEnablementSetParams{
		Enablement: map[string]bool{"known": true},
		Enabled:    []string{"legacy-enabled"},
		Disabled:   []string{"legacy-disabled"},
	})
	if _, ok := enablementPayload["enabled"]; ok {
		t.Fatalf("legacy enabled should not be emitted: %#v", enablementPayload)
	}
	if _, ok := enablementPayload["disabled"]; ok {
		t.Fatalf("legacy disabled should not be emitted: %#v", enablementPayload)
	}
	enablement := enablementPayload["enablement"].(map[string]any)
	if enablement["known"] != true {
		t.Fatalf("enablement payload = %#v", enablementPayload)
	}

	responsePayload := marshalObjectForTest(t, &FeatureEnablementSetResponse{Enablement: map[string]bool{"known": false}})
	responseEnablement := responsePayload["enablement"].(map[string]any)
	if responseEnablement["known"] != false {
		t.Fatalf("enablement response payload = %#v", responsePayload)
	}
}

func TestDefaultFeatureCatalogUsesRustExperimentalMenuMetadata(t *testing.T) {
	catalog := DefaultFeatureCatalog()
	var memories FeatureEntry
	found := false
	for _, entry := range catalog {
		if entry.Key == "memories" {
			memories = entry
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("memories feature missing from catalog: %#v", catalog)
	}
	if memories.DisplayName == nil || *memories.DisplayName != "Memories" {
		t.Fatalf("memories display name = %#v", memories.DisplayName)
	}
	if memories.Description == nil || *memories.Description != "Allow Codex to create new memories from conversations and bring relevant memories into new conversations." {
		t.Fatalf("memories description = %#v", memories.Description)
	}
	if memories.Announcement == nil || *memories.Announcement != "NEW: Codex can now generate and use memories. Try it now with `/memories`" {
		t.Fatalf("memories announcement = %#v", memories.Announcement)
	}
}

func marshalObjectForTest(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return payload
}
