package sandbox

import "testing"

func TestLegacySandboxPolicyLabelCanonicalProfiles(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"type":"disabled"}`, "danger-full-access"},
		{`{"type":"external","network":"restricted"}`, "external-sandbox"},
		{`{"type":"managed","network":"restricted","file_system":{"type":"unrestricted"}}`, "external-sandbox"},
		{`{"type":"managed","network":"enabled","file_system":{"type":"unrestricted"}}`, "danger-full-access"},
		{`{"type":"managed","network":"restricted","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots"}},"access":"write"}]}}`, "workspace-write"},
		{`{"type":"managed","network":"restricted","file_system":{"type":"restricted","entries":[]}}`, "read-only"},
	} {
		profile, err := ParseRuntimePermissionProfileJSON(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := profile.LegacySandboxPolicyLabel(t.TempDir()); got != tc.want {
			t.Errorf("label(%s)=%s, want %s", tc.raw, got, tc.want)
		}
	}
}
