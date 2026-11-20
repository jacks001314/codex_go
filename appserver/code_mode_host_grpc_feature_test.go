package appserver

import (
	"testing"

	"codex_go/codemode"
)

// Mirrors Rust #62476 (336dc645fb): the legacy framed stdio protocol is gone,
// so the process-owned host always uses gRPC over stdio and both values of the
// deprecated code_mode_host_grpc setting are ignored.
func TestCodeModeHostGrpcFeatureIsIgnoredLikeRust(t *testing.T) {
	for _, enablement := range []map[string]bool{
		nil,
		{"code_mode_host_grpc": true},
		{"code_mode_host_grpc": false},
	} {
		provider := newRuntimeCodeModeProcessProvider(&RuntimeRouterOptions{FeatureEnablement: enablement}, "missing-host")
		wrapped, ok := provider.(*codemode.LocalServerOutputProvider)
		if !ok {
			t.Fatalf("provider = %T, want host-side output truncation by default", provider)
		}
		if _, ok := wrapped.Inner.(*codemode.GrpcProcessProvider); !ok {
			t.Fatalf("enablement %v inner provider = %T, want the gRPC process provider", enablement, wrapped.Inner)
		}

		plain := newRuntimeCodeModeProcessProvider(&RuntimeRouterOptions{
			FeatureEnablement: map[string]bool{"code_mode_host_output_truncation": false},
		}, "missing-host")
		if _, ok := plain.(*codemode.GrpcProcessProvider); !ok {
			t.Fatalf("enablement %v provider = %T, want the unwrapped gRPC provider", enablement, plain)
		}
	}
}
