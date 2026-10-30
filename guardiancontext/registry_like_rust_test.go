package guardiancontext

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"codex_go/eventmap"
)

// testContributor is the Go counterpart of the crate's registry test
// contributor: it records every scope-filtered invocation and returns a fixed
// outcome.
type testContributor struct {
	outcome     *string
	err         error
	scope       SectionScope
	invocations *int
}

// Scope implements SectionContributor.
func (c testContributor) Scope() SectionScope { return c.scope }

// Contribute implements SectionContributor.
func (c testContributor) Contribute(input SectionInput) (*ContextSection, error) {
	*c.invocations = *c.invocations + 1
	if c.err != nil {
		return nil, c.err
	}
	if c.outcome == nil {
		return nil, nil
	}
	section := testSection(*c.outcome, len(input.History))
	return &section, nil
}

func testSection(label string, historyLen int) ContextSection {
	text := fmt.Sprintf("%s: history items: %d", label, historyLen)
	return ContextSection{Items: []ConversationTranscriptEntry{{
		Kind:          UserKind(),
		Text:          text,
		OriginalBytes: len(text),
	}}}
}

// Mirrors registry_collects_target_specific_sections_in_registration_order.
func TestRegistryCollectsTargetSpecificSectionsInRegistrationOrderLikeRust(t *testing.T) {
	specs := []struct {
		label string
		scope SectionScope
	}{
		{"root", SectionScopeShared},
		{"permissions", SectionScopeSyncOnly},
		{"reviews", SectionScopeAsyncOnly},
		{"action", SectionScopeShared},
	}
	var registry SectionRegistry
	counts := make([]int, len(specs))
	for i, spec := range specs {
		label := spec.label
		registry.Register(testContributor{outcome: &label, scope: spec.scope, invocations: &counts[i]})
	}
	history := []eventmap.ResponseItem{{Kind: eventmap.ResponseMessage, Role: "user"}}

	syncSections, err := registry.Collect(SectionInput{Target: ContextTargetSync, History: history})
	if err != nil {
		t.Fatalf("sync Collect: %v", err)
	}
	asyncSections, err := registry.Collect(SectionInput{Target: ContextTargetAsync, History: history})
	if err != nil {
		t.Fatalf("async Collect: %v", err)
	}

	if want := []ContextSection{
		testSection("root", 1),
		testSection("permissions", 1),
		testSection("action", 1),
	}; !reflect.DeepEqual(syncSections, want) {
		t.Fatalf("sync sections = %#v, want %#v", syncSections, want)
	}
	if want := []ContextSection{
		testSection("root", 1),
		testSection("reviews", 1),
		testSection("action", 1),
	}; !reflect.DeepEqual(asyncSections, want) {
		t.Fatalf("async sections = %#v, want %#v", asyncSections, want)
	}
	if want := []int{2, 1, 1, 2}; !reflect.DeepEqual(counts, want) {
		t.Fatalf("invocations = %v, want %v", counts, want)
	}
}

// Mirrors registry_skips_optional_sections_and_stops_on_missing_required_evidence.
func TestRegistrySkipsOptionalSectionsAndStopsOnMissingRequiredEvidenceLikeRust(t *testing.T) {
	missing := MissingRequiredEvidence("permissions")
	for _, target := range []ContextTarget{ContextTargetSync, ContextTargetAsync} {
		root, action := "root", "action"
		specs := []struct {
			outcome *string
			err     error
		}{
			{outcome: &root},
			{},
			{err: missing},
			{outcome: &action},
		}
		var registry SectionRegistry
		counts := make([]int, len(specs))
		for i, spec := range specs {
			registry.Register(testContributor{
				outcome:     spec.outcome,
				err:         spec.err,
				scope:       SectionScopeShared,
				invocations: &counts[i],
			})
		}

		sections, err := registry.Collect(SectionInput{
			Target:  target,
			History: []eventmap.ResponseItem{{Kind: eventmap.ResponseMessage}},
		})
		if !errors.Is(err, ErrMissingRequiredEvidence) {
			t.Fatalf("target %q error = %v, want missing required evidence", target, err)
		}
		var sectionErr *SectionError
		if !errors.As(err, &sectionErr) || sectionErr.Section != "permissions" {
			t.Fatalf("target %q error = %v, want section permissions", target, err)
		}
		if sections != nil {
			t.Fatalf("target %q returned partial context: %#v", target, sections)
		}
		if want := []int{1, 1, 1, 0}; !reflect.DeepEqual(counts, want) {
			t.Fatalf("target %q invocations = %v, want %v", target, counts, want)
		}
	}
}

// Mirrors SectionScope::includes.
func TestSectionScopeIncludesLikeRust(t *testing.T) {
	cases := []struct {
		scope SectionScope
		want  map[ContextTarget]bool
	}{
		{SectionScopeShared, map[ContextTarget]bool{ContextTargetSync: true, ContextTargetAsync: true}},
		{SectionScopeSyncOnly, map[ContextTarget]bool{ContextTargetSync: true, ContextTargetAsync: false}},
		{SectionScopeAsyncOnly, map[ContextTarget]bool{ContextTargetSync: false, ContextTargetAsync: true}},
	}
	for _, tc := range cases {
		for target, want := range tc.want {
			if got := tc.scope.Includes(target); got != want {
				t.Errorf("scope %q Includes(%q) = %t, want %t", tc.scope, target, got, want)
			}
		}
	}
}

// A registry keeps registration order across collections without retaining
// per-collection state.
func TestRegistryCollectIsRepeatableLikeRust(t *testing.T) {
	root, permissions := "root", "permissions"
	var registry SectionRegistry
	calls := make([]int, 2)
	registry.Register(testContributor{outcome: &root, scope: SectionScopeShared, invocations: &calls[0]})
	registry.Register(testContributor{outcome: &permissions, scope: SectionScopeSyncOnly, invocations: &calls[1]})

	for range 2 {
		sections, err := registry.Collect(SectionInput{Target: ContextTargetSync})
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if want := []ContextSection{testSection("root", 0), testSection("permissions", 0)}; !reflect.DeepEqual(sections, want) {
			t.Fatalf("sections = %#v, want %#v", sections, want)
		}
	}
	if want := []int{2, 2}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("invocations = %v, want %v", calls, want)
	}
}
