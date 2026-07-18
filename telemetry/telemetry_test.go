package telemetry

import (
	"context"
	"testing"
)

func TestEnvFilterUsesDefault(t *testing.T) {
	t.Setenv("RUST_LOG", "")
	t.Setenv("CODEX_LOG", "")
	if EnvFilter() != DefaultLogFilter {
		t.Fatalf("unexpected default filter: %q", EnvFilter())
	}
}

func TestRunUntilShutdownRunsCallback(t *testing.T) {
	called := false
	err := RunUntilShutdown(context.Background(), func(ctx context.Context) error {
		called = ctx != nil
		return nil
	})
	if err != nil || !called {
		t.Fatalf("RunUntilShutdown() = %v called=%v", err, called)
	}
}
