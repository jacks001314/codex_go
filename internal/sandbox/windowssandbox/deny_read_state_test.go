package windowssandbox

import (
	"path/filepath"
	"testing"
)

func TestDenyReadACLStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := &persistentDenyReadACLState{
		Principals: map[string][]string{
			"S-1-5-21-1-2-3-4": []string{`C:\secret`},
		},
	}
	if err := storeDenyReadACLState(path, state); err != nil {
		t.Fatalf("storeDenyReadACLState() error = %v", err)
	}
	got, err := loadDenyReadACLState(path)
	if err != nil {
		t.Fatalf("loadDenyReadACLState() error = %v", err)
	}
	paths := got.Principals["S-1-5-21-1-2-3-4"]
	if len(paths) != 1 || paths[0] != `C:\secret` {
		t.Fatalf("loaded state = %#v, want path round trip", got)
	}
}
