package appserver

import (
	"os"
	"testing"

	"codex_go/config"
	"codex_go/features"
)

// Rust #57864: a sample inherits the parent's interrupt_on_cancel setting but
// may override it per request.
func TestThreadSampleInterruptOnCancelOverrideLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\ninterrupt_on_cancel = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	enabled := features.Enabled(cfg.FeatureSettings(), "interrupt_on_cancel")
	if !enabled {
		t.Fatalf("interrupt_on_cancel should be enabled by the written config")
	}

	tru := true
	fals := false
	if got := threadSampleInterruptOnCancel(cfg, nil); got != enabled {
		t.Fatalf("inherited interrupt_on_cancel = %v, want %v", got, enabled)
	}
	if got := threadSampleInterruptOnCancel(cfg, &ThreadSampleParams{InterruptOnCancel: &fals}); got {
		t.Fatalf("explicit false override = %v, want false", got)
	}
	if got := threadSampleInterruptOnCancel(nil, &ThreadSampleParams{InterruptOnCancel: &tru}); !got {
		t.Fatalf("explicit true override without config = %v, want true", got)
	}
	if got := threadSampleInterruptOnCancel(nil, nil); got {
		t.Fatalf("missing config inherits off = %v, want false", got)
	}
}
