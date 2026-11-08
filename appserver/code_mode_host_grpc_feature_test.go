package appserver

import (
	"testing"

	"codex_go/codemode"
)

// Mirrors Rust #59973 (247934cfe5): gRPC over stdio and host-side output
// truncation are the defaults for the process-owned code-mode host, and an
// explicit opt-out in either gate keeps the legacy behaviour. Rust #52723
// introduced the gRPC gate as disabled-by-default; the default flipped later.
func TestCodeModeHostGrpcFeatureSelectsProcessProviderLikeRust(t *testing.T) {
	provider := newRuntimeCodeModeProcessProvider(&RuntimeRouterOptions{}, "missing-host")
	wrapped, ok := provider.(*codemode.LocalServerOutputProvider)
	if !ok {
		t.Fatalf("provider = %T, want host-side output truncation by default", provider)
	}
	if _, ok := wrapped.Inner.(*codemode.GrpcProcessProvider); !ok {
		t.Fatalf("inner provider = %T, want the gRPC process provider", wrapped.Inner)
	}

	legacy := newRuntimeCodeModeProcessProvider(&RuntimeRouterOptions{
		FeatureEnablement: map[string]bool{
			"code_mode_host_grpc":              false,
			"code_mode_host_output_truncation": false,
		},
	}, "missing-host")
	if _, ok := legacy.(*codemode.ProcessProvider); !ok {
		t.Fatalf("provider = %T, want the legacy stdio provider", legacy)
	}

	truncated := newRuntimeCodeModeProcessProvider(&RuntimeRouterOptions{
		FeatureEnablement: map[string]bool{"code_mode_host_grpc": false},
	}, "missing-host")
	wrapped, ok = truncated.(*codemode.LocalServerOutputProvider)
	if !ok {
		t.Fatalf("provider = %T, want truncation to stay independent of the transport", truncated)
	}
	if _, ok := wrapped.Inner.(*codemode.ProcessProvider); !ok {
		t.Fatalf("inner provider = %T, want stdio", wrapped.Inner)
	}
}
