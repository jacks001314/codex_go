package tool

import (
	"context"
	"testing"
)

func TestRegistryRegistrationCountsGroupExposuresLikeRust(t *testing.T) {
	registry := NewRegistry()
	register := func(name string, exposure Exposure, count int) {
		t.Helper()
		for index := 0; index < count; index++ {
			toolName := name
			if count > 1 {
				toolName += string(rune('a' + index))
			}
			err := registry.Register(NewExecutorFunc(
				Spec{Name: PlainName(toolName), Exposure: exposure},
				func(context.Context, *Invocation) (*Output, error) { return &Output{}, nil },
			))
			if err != nil {
				t.Fatalf("Register(%s) error = %v", toolName, err)
			}
		}
	}

	register("default", "", 1)
	register("model_visible", ExposureModelVisible, 2)
	register("direct_model_only", ExposureDirectModelOnly, 3)
	register("discoverable", ExposureDiscoverable, 4)
	register("deferred_model_only", ExposureDeferredModelOnly, 5)
	register("code_mode_only", ExposureCodeModeOnly, 6)
	register("hidden", ExposureHidden, 7)

	want := RegistrationCounts{
		Direct:            3,
		DirectModelOnly:   3,
		Deferred:          4,
		DeferredModelOnly: 5,
		CodeModeOnly:      6,
		Hidden:            7,
	}
	if got := registry.RegistrationCounts(); got != want {
		t.Fatalf("RegistrationCounts() = %#v, want %#v", got, want)
	}
	if got := NewRouter(registry).RegistrationCounts(); got != want {
		t.Fatalf("Router.RegistrationCounts() = %#v, want %#v", got, want)
	}

	var nilRegistry *Registry
	if got := nilRegistry.RegistrationCounts(); got != (RegistrationCounts{}) {
		t.Fatalf("nil Registry.RegistrationCounts() = %#v, want zero", got)
	}
	var nilRouter *Router
	if got := nilRouter.RegistrationCounts(); got != (RegistrationCounts{}) {
		t.Fatalf("nil Router.RegistrationCounts() = %#v, want zero", got)
	}
}
