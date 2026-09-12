package app

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"codex_go/appserver"
	"codex_go/features"
)

// TestFetchExperimentalFeaturesMatchesRust covers Rust
// experimental_features::fetch: the thread-scoped, 100-per-page request shape,
// duplicate-name dropping, wire mapping, and the two error strings the popup
// shows.
func TestFetchExperimentalFeaturesMatchesRust(t *testing.T) {
	var requests []features.FeatureListParams
	displayName := "Display"
	description := "Description"
	request := func(_ context.Context, method appserver.Method, params any, target any) error {
		if method != appserver.MethodExperimentalFeatureList {
			t.Fatalf("method = %s", method)
		}
		page := params.(features.FeatureListParams)
		requests = append(requests, page)
		switch len(requests) {
		case 1:
			if page.Cursor != nil {
				t.Fatalf("first page cursor = %q", *page.Cursor)
			}
			if page.ThreadID == nil || *page.ThreadID != "t-1" {
				t.Fatalf("thread id = %#v", page.ThreadID)
			}
			if page.Limit == nil || *page.Limit != experimentalFeaturesPageLimit {
				t.Fatalf("limit = %#v", page.Limit)
			}
			next := "1"
			*(target.(*features.FeatureListResponse)) = features.FeatureListResponse{
				Data: []features.FeatureEntry{
					{Key: "alpha", DisplayName: &displayName, Description: &description, Stage: features.FeatureStageBeta, Enabled: true, DefaultEnabled: true},
					{Key: "alpha", Stage: features.FeatureStageStable},
					{Key: "", Stage: features.FeatureStageBeta},
				},
				NextCursor: &next,
			}
		case 2:
			if page.Cursor == nil || *page.Cursor != "1" {
				t.Fatalf("second page cursor = %#v", page.Cursor)
			}
			*(target.(*features.FeatureListResponse)) = features.FeatureListResponse{
				Data: []features.FeatureEntry{{Key: "gamma", Stage: features.FeatureStageStable, Enabled: true}},
			}
		default:
			t.Fatalf("unexpected page %d", len(requests))
		}
		return nil
	}

	entries, err := fetchExperimentalFeatures(context.Background(), request, " t-1 ")
	if err != nil {
		t.Fatalf("fetchExperimentalFeatures() error = %v", err)
	}
	if len(requests) != 2 || len(entries) != 2 {
		t.Fatalf("requests = %d entries = %#v", len(requests), entries)
	}
	if entries[0].Name != "alpha" || entries[0].DisplayName != "Display" || entries[0].Description != "Description" ||
		!entries[0].Enabled || !entries[0].DefaultEnabled || entries[0].Stage != "beta" {
		t.Fatalf("alpha entry = %#v", entries[0])
	}
	if entries[1].Name != "gamma" || entries[1].Stage != "stable" || !entries[1].Enabled {
		t.Fatalf("gamma entry = %#v", entries[1])
	}
}

// TestFetchExperimentalFeaturesGuardsMatchRust covers the repeated-cursor,
// page-size, page-count, and transport-failure guards.
func TestFetchExperimentalFeaturesGuardsMatchRust(t *testing.T) {
	responseWith := func(entries int, next string) func(context.Context, appserver.Method, any, any) error {
		return func(_ context.Context, _ appserver.Method, _ any, target any) error {
			data := make([]features.FeatureEntry, entries)
			for index := range data {
				data[index] = features.FeatureEntry{Key: "feature-" + strconv.Itoa(index), Stage: features.FeatureStageBeta}
			}
			response := features.FeatureListResponse{Data: data}
			if next != "" {
				value := next
				response.NextCursor = &value
			}
			*(target.(*features.FeatureListResponse)) = response
			return nil
		}
	}
	if _, err := fetchExperimentalFeatures(context.Background(), nil, "t"); err == nil ||
		err.Error() != "Experimental feature request failed" {
		t.Fatalf("nil request error = %v", err)
	}
	if _, err := fetchExperimentalFeatures(context.Background(), responseWith(experimentalFeaturesPageLimit+1, ""), "t"); err == nil ||
		err.Error() != "Experimental feature page exceeds requested limit" {
		t.Fatalf("oversized page error = %v", err)
	}
	if _, err := fetchExperimentalFeatures(context.Background(), responseWith(0, "same"), "t"); err == nil ||
		err.Error() != "Experimental feature pagination repeated a cursor" {
		t.Fatalf("repeated cursor error = %v", err)
	}
	paged := func(_ context.Context, _ appserver.Method, params any, target any) error {
		page := params.(features.FeatureListParams)
		next := "cursor-0"
		if page.Cursor != nil {
			index, _ := strconv.Atoi((*page.Cursor)[len("cursor-"):])
			next = "cursor-" + strconv.Itoa(index+1)
		}
		value := next
		*(target.(*features.FeatureListResponse)) = features.FeatureListResponse{
			Data:       []features.FeatureEntry{{Key: "feature-" + next, Stage: features.FeatureStageBeta}},
			NextCursor: &value,
		}
		return nil
	}
	if _, err := fetchExperimentalFeatures(context.Background(), paged, "t"); err == nil ||
		err.Error() != "Experimental feature discovery exceeded 10 pages" {
		t.Fatalf("page bound error = %v", err)
	}
	failing := func(context.Context, appserver.Method, any, any) error { return errors.New("offline") }
	if _, err := fetchExperimentalFeatures(context.Background(), failing, "t"); err == nil ||
		err.Error() != "Experimental feature request failed" {
		t.Fatalf("transport error = %v", err)
	}
}
