//go:build windows

package windowssandbox

import (
	"fmt"
	"testing"
	"time"
)

func TestLookupLocalUserSIDReportsMissingAccount(t *testing.T) {
	name := fmt.Sprintf("codex-missing-%d", time.Now().UnixNano())
	sid, exists, err := lookupLocalUserSID(name)
	if err != nil {
		t.Fatalf("lookupLocalUserSID(%q) error = %v", name, err)
	}
	if exists || sid != "" {
		t.Fatalf("missing account resolved as %q, %v", sid, exists)
	}
}
