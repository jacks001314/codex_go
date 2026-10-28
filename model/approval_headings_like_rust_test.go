package model

import (
	"encoding/json"
	"testing"
)

func TestApprovalMessagesAcceptRequestPermissionsAliasLikeRust(t *testing.T) {
	for _, field := range []string{"request_permissions", "request_permissions_tool"} {
		data := []byte(`{"` + field + `":"Permission requests","auto_review_suffix":"Reviewed"}`)
		var messages ApprovalMessages
		if err := json.Unmarshal(data, &messages); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", field, err)
		}
		if messages.RequestPermissions == nil || *messages.RequestPermissions != "Permission requests" {
			t.Fatalf("%s RequestPermissions = %#v", field, messages.RequestPermissions)
		}
		if messages.AutoReviewSuffix == nil || *messages.AutoReviewSuffix != "Reviewed" {
			t.Fatalf("%s AutoReviewSuffix = %#v", field, messages.AutoReviewSuffix)
		}
	}

	var sparse ApprovalMessages
	if err := json.Unmarshal([]byte(`{"on_request":"x"}`), &sparse); err != nil {
		t.Fatalf("Unmarshal(sparse) error = %v", err)
	}
	if sparse.RequestPermissions != nil || sparse.AutoReviewSuffix != nil {
		t.Fatalf("sparse approvals = %+v", sparse)
	}

	var permissions PermissionMessages
	if err := json.Unmarshal([]byte(`{"approved_command_prefix_saved":"Saved"}`), &permissions); err != nil {
		t.Fatalf("Unmarshal(permissions) error = %v", err)
	}
	if permissions.ApprovedCommandPrefixSaved == nil || *permissions.ApprovedCommandPrefixSaved != "Saved" {
		t.Fatalf("ApprovedCommandPrefixSaved = %#v", permissions.ApprovedCommandPrefixSaved)
	}
}
