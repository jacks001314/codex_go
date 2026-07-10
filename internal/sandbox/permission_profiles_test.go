package sandbox

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestListProfilesPaginates(t *testing.T) {
	limit := 2
	service := NewPermissionProfileService(nil)
	first, err := service.List(&PermissionProfileListParams{Limit: &limit})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(first.Data) != 2 || first.NextCursor == nil {
		t.Fatalf("first = %#v", first)
	}
	second, err := service.List(&PermissionProfileListParams{Cursor: first.NextCursor, Limit: &limit})
	if err != nil {
		t.Fatalf("List(second) error = %v", err)
	}
	if len(second.Data) == 0 {
		t.Fatalf("second = %#v", second)
	}
}

func TestListProfilesRustBuiltinOrderAndShape(t *testing.T) {
	service := NewPermissionProfileService(nil)
	got, err := service.List(&PermissionProfileListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got.Data) != 3 {
		t.Fatalf("profiles = %#v", got.Data)
	}
	for i, want := range []string{":read-only", ":workspace", ":danger-full-access"} {
		if got.Data[i].ID != want || got.Data[i].Description != "" || !got.Data[i].Allowed {
			t.Fatalf("profile[%d] = %#v, want id %q with nil description and allowed", i, got.Data[i], want)
		}
	}
}

func TestListProfilesRejectsBadCursor(t *testing.T) {
	service := NewPermissionProfileService(nil)
	cursor := "bad"
	if _, err := service.List(&PermissionProfileListParams{Cursor: &cursor}); !errors.Is(err, ErrInvalidPermissionProfileRequest) {
		t.Fatalf("List(bad cursor) error = %v, want ErrInvalidPermissionProfileRequest", err)
	} else if err.Error() != "invalid permission profile request: invalid cursor: bad" {
		t.Fatalf("List(bad cursor) error = %v", err)
	}
}

func TestPermissionProfileSummaryMarshalRustShape(t *testing.T) {
	summary := ProfileFromSandbox(":workspace", "Workspace writes.", NewWorkspaceWritePolicy())
	data, err := json.Marshal(&summary)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, legacyKey := range []string{"sandboxMode", "network"} {
		if _, ok := payload[legacyKey]; ok {
			t.Fatalf("legacy key %q should not be emitted: %#v", legacyKey, payload)
		}
	}
	if payload["id"] != ":workspace" || payload["description"] != "Workspace writes." || payload["allowed"] != true {
		t.Fatalf("payload = %#v", payload)
	}
}
