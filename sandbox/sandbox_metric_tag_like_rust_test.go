package sandbox

import "testing"

// Rust SandboxType::as_metric_tag (#47568) uses stable analytics labels that
// differ from the camelCase executor-protocol spelling.
func TestSandboxTypeMetricTagLikeRust(t *testing.T) {
	cases := []struct {
		sandboxType SandboxType
		want        string
	}{
		{SandboxTypeNone, "none"},
		{SandboxTypeMacosSeatbelt, "seatbelt"},
		{SandboxTypeLinuxSeccomp, "seccomp"},
		{SandboxTypeWindowsRestrictedToken, "windows_sandbox"},
		{SandboxTypeWindowsMxc, "windows_mxc"},
	}
	for _, tc := range cases {
		if got := tc.sandboxType.MetricTag(); got != tc.want {
			t.Fatalf("MetricTag(%q) = %q, want %q", tc.sandboxType, got, tc.want)
		}
	}
}
