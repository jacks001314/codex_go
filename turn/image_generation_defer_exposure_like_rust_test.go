package turn

import (
	"testing"

	"codex_go/tool"
)

// Mirrors Rust #63594: with defer_image_generation the tool keeps its
// definition out of the initial model request while Code Mode still discovers
// it through ALL_TOOLS.
func TestImageGenerationDeferredExposureLikeRust(t *testing.T) {
	direct := NewImageGenerationHandler(&ImageGenerationOptions{})
	spec := direct.Spec()
	if spec.Exposure != tool.ExposureModelVisible {
		t.Fatalf("direct exposure = %q, want model visible", spec.Exposure)
	}
	deferred := NewImageGenerationHandler(&ImageGenerationOptions{DeferExposure: true})
	deferredSpec := deferred.Spec()
	if deferredSpec.Exposure != tool.ExposureDiscoverable {
		t.Fatalf("deferred exposure = %q, want discoverable", deferredSpec.Exposure)
	}
	if deferredSpec.Name != spec.Name {
		t.Fatalf("deferred tool renamed: %q vs %q", deferredSpec.Name, spec.Name)
	}

	registry := tool.NewRegistry()
	if err := registry.Register(deferred); err != nil {
		t.Fatal(err)
	}
	exec, wait := tool.NewCodeModeExecutors(registry, tool.PlainName(tool.DefaultExecCommandToolName))
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatal(err)
	}
	key := deferredSpec.Name.Key()
	for _, visible := range registry.ModelVisibleSpecs() {
		if visible.Name.Key() == key {
			t.Fatalf("deferred tool %q stayed in the initial model request", key)
		}
	}
	discoverable := false
	for _, metadata := range tool.NewRouter(registry).CodeModeToolNames() {
		if metadata.Name == ImageGenerationToolName && metadata.Namespace != nil && *metadata.Namespace == ImageGenerationNamespace {
			discoverable = true
		}
	}
	if !discoverable {
		t.Fatalf("deferred tool %q is not discoverable in Code Mode", key)
	}
}
