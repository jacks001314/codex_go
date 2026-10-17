package appserver

import (
	"testing"

	"codex_go/codemode"
)

// Mirrors Rust #52723: the disabled-by-default code_mode_host_grpc feature
// selects the gRPC-over-stdio provider; otherwise the legacy stdio provider
// stays in place.
func TestCodeModeHostGrpcFeatureSelectsProcessProviderLikeRust(t *testing.T) {
	if _, ok := newRuntimeCodeModeProcessProvider(nil, "missing-host").(*codemode.GrpcProcessProvider); ok {
		t.Fatal("the default provider unexpectedly used gRPC over stdio")
	}
	provider := newRuntimeCodeModeProcessProvider(&RuntimeRouterOptions{
		FeatureEnablement: map[string]bool{"code_mode_host_grpc": true},
	}, "missing-host")
	if _, ok := provider.(*codemode.GrpcProcessProvider); !ok {
		t.Fatalf("provider = %T, want the gRPC process provider", provider)
	}
}
