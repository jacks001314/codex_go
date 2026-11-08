package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/session"
)

// Mirrors Rust #59958: a thread exposes the catalog entry its model resolves to,
// so a turn that falls back to synthetic metadata can explain why this model was
// used. The value is derived from the persisted record, so every thread surface
// (read/start/resume) reports the same entry.
func TestThreadSelectedModelInfoUsesThreadCatalogLikeRust(t *testing.T) {
	catalog := model.BundledModelsResponse()
	if len(catalog.Models) == 0 || strings.TrimSpace(catalog.Models[0].Slug) == "" {
		t.Skip("bundled catalog is empty")
	}
	slug := catalog.Models[0].Slug
	record := &session.Record{ID: "thread-1"}
	record.Metadata.Model = slug
	thread := BuildThread(record, "", false)
	if thread == nil || thread.SelectedModelInfo == nil {
		t.Fatalf("BuildThread() selected model info = %#v, want the catalog entry", thread)
	}
	info := thread.SelectedModelInfo
	if info.Slug != slug {
		t.Fatalf("slug = %q, want the thread's model", info.Slug)
	}
	if info.DisplayName == "" {
		t.Fatalf("display name missing for a catalog model: %#v", info)
	}
	if info.UsedFallbackModelMetadata {
		t.Fatalf("catalog model reported fallback metadata: %#v", info)
	}

	// A slug the catalog does not know still resolves, and says so: that is the
	// case turn/start's fallback warning explains.
	record.Metadata.Model = "not-a-catalog-model"
	unknown := BuildThread(record, "", false)
	if unknown.SelectedModelInfo == nil || !unknown.SelectedModelInfo.UsedFallbackModelMetadata {
		t.Fatalf("unknown model info = %#v, want fallback metadata", unknown.SelectedModelInfo)
	}
	if unknown.SelectedModelInfo.Slug != "not-a-catalog-model" {
		t.Fatalf("unknown slug = %q, want the requested model", unknown.SelectedModelInfo.Slug)
	}

	// A live settings overlay rewrites the model, so it rewrites the catalog
	// entry with it: the thread that was persisted with an unknown model must
	// report the catalog entry of the model it is running on now.
	overlaid := BuildThread(record, "", false)
	applyThreadModelSettings(overlaid, &Settings{Model: slug})
	if overlaid.SelectedModelInfo == nil || overlaid.SelectedModelInfo.Slug != slug {
		t.Fatalf("overlaid info = %#v, want the live model's catalog entry", overlaid.SelectedModelInfo)
	}
	if overlaid.SelectedModelInfo.UsedFallbackModelMetadata {
		t.Fatalf("overlaid info kept fallback metadata for a catalog model: %#v", overlaid.SelectedModelInfo)
	}

	// The field is part of the thread wire shape, and a thread without a model
	// keeps it absent rather than emitting an empty object.
	encoded, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if wire["selectedModelInfo"] == nil {
		t.Fatalf("thread wire = %s, want selectedModelInfo", encoded)
	}
	empty := BuildThread(&session.Record{ID: "thread-2"}, "", false)
	if empty.SelectedModelInfo != nil {
		t.Fatalf("modelless thread info = %#v, want nil", empty.SelectedModelInfo)
	}
}
