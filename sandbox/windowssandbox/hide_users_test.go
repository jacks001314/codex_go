package windowssandbox

import "testing"

func TestHideNewlyCreatedUsersNoopsForEmptyList(t *testing.T) {
	if err := HideNewlyCreatedUsers(nil, ""); err != nil {
		t.Fatalf("HideNewlyCreatedUsers(nil) error = %v", err)
	}
}
