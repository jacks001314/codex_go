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
	if _, err := service.SetEnablement(&FeatureEnablementSetParams{Enabled: []string{"known", "missing"}}); err != nil {
		t.Fatalf("SetEnablement() error = %v", err)
	}
	response, err := service.List(&FeatureListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !response.Data[0].Enabled {
		t.Fatalf("expected known feature enabled")
	}
	if _, err := service.SetEnablement(&FeatureEnablementSetParams{Enablement: map[string]bool{"known": false, "missing": true}}); err != nil {
		t.Fatalf("SetEnablement(map) error = %v", err)
	}
	response, err = service.List(&FeatureListParams{})
	if err != nil {
		t.Fatalf("List(after map) error = %v", err)
	}
	if response.Data[0].Enabled {
		t.Fatalf("expected known feature disabled")
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
