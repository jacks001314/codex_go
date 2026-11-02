package appserver

import (
	"testing"

	"codex_go/model"
)

// Rust Session::maybe_emit_model_warnings_for_turn emits this warning whenever
// the turn's ModelInfo came from the synthetic fallback catalog entry.
func TestTurnStartWarnsForFallbackModelMetadataLikeRust(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	router.SetNotificationSink(sink)

	router.maybeEmitModelMetadataWarning("thread-1", &appTurnRunConfig{
		Model:       "deepseek-v4-flash",
		ModelInfo:   &model.ModelInfo{Slug: "deepseek-v4-flash", UsedFallbackModelMetadata: true},
		ModelOrigin: model.ModelOriginSynthetic,
	})

	warnings := warningNotificationsForTest(sink)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want 1", warnings)
	}
	want := "Model metadata for `deepseek-v4-flash` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
	if warnings[0].Message != want {
		t.Fatalf("message = %q, want %q", warnings[0].Message, want)
	}
	if warnings[0].ThreadID == nil || *warnings[0].ThreadID != "thread-1" {
		t.Fatalf("thread id = %v, want thread-1", warnings[0].ThreadID)
	}
}

func TestTurnStartSkipsModelMetadataWarningForCatalogModelLikeRust(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	router.SetNotificationSink(sink)

	router.maybeEmitModelMetadataWarning("thread-1", &appTurnRunConfig{
		Model:       "gpt-5.1-codex",
		ModelInfo:   &model.ModelInfo{Slug: "gpt-5.1-codex"},
		ModelOrigin: model.ModelOriginBundled,
	})
	if warnings := warningNotificationsForTest(sink); len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
}

// The origin and the fallback flag are independent signals: either one marks
// synthesized metadata, and a missing model name still stays silent.
func TestTurnStartModelMetadataWarningSignals(t *testing.T) {
	cases := []struct {
		name       string
		runConfig  *appTurnRunConfig
		wantWarned bool
	}{
		{
			name: "synthetic origin without fallback flag",
			runConfig: &appTurnRunConfig{
				Model:       "custom-model",
				ModelInfo:   &model.ModelInfo{Slug: "custom-model"},
				ModelOrigin: model.ModelOriginSynthetic,
			},
			wantWarned: true,
		},
		{
			name: "fallback flag without origin",
			runConfig: &appTurnRunConfig{
				Model:     "custom-model",
				ModelInfo: &model.ModelInfo{Slug: "custom-model", UsedFallbackModelMetadata: true},
			},
			wantWarned: true,
		},
		{
			name: "bundled origin",
			runConfig: &appTurnRunConfig{
				Model:       "bundled-model",
				ModelInfo:   &model.ModelInfo{Slug: "bundled-model"},
				ModelOrigin: model.ModelOriginBundled,
			},
			wantWarned: false,
		},
		{
			name: "no model info",
			runConfig: &appTurnRunConfig{
				ModelOrigin: model.ModelOriginSynthetic,
			},
			wantWarned: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := NewNotificationBuffer()
			router := NewRuntimeRouter(RuntimeServices{})
			router.SetNotificationSink(sink)
			router.maybeEmitModelMetadataWarning("thread-1", tc.runConfig)
			warnings := warningNotificationsForTest(sink)
			if tc.wantWarned && len(warnings) != 1 {
				t.Fatalf("warnings = %+v, want 1", warnings)
			}
			if !tc.wantWarned && len(warnings) != 0 {
				t.Fatalf("warnings = %+v, want none", warnings)
			}
		})
	}
}
