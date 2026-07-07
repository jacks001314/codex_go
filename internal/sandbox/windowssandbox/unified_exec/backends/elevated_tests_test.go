package backends

import "testing"

func TestCommonBackendKind(t *testing.T) {
	if CommonBackendKind(true) != BackendElevated {
		t.Fatalf("elevated kind mismatch")
	}
	if CommonBackendKind(false) != BackendLegacy {
		t.Fatalf("legacy kind mismatch")
	}
}
