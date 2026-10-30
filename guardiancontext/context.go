// Package guardiancontext assembles structured context shared by synchronous
// Guardian review and asynchronous scoring.
//
// Contributor failures abort collection without returning partial context.
// Sections carry structured transcript evidence without depending on either
// consumer's rendering, retention, compaction, or request lifecycle.
// Registered contributors declare their scope once and are collected only for
// matching context consumers.
package guardiancontext

import (
	"errors"
	"fmt"

	"codex_go/eventmap"
)

// ContextTarget is the consumer for which a Guardian context is composed.
type ContextTarget string

const (
	// ContextTargetSync is the reusable synchronous Guardian reviewer.
	ContextTargetSync ContextTarget = "sync"
	// ContextTargetAsync is the asynchronous Guardian action scorer.
	ContextTargetAsync ContextTarget = "async"
)

// SectionScope declares the consumers to which a context section contributes.
type SectionScope string

const (
	// SectionScopeShared includes the section in both synchronous review and
	// asynchronous scoring.
	SectionScopeShared SectionScope = "shared"
	// SectionScopeSyncOnly includes the section only in synchronous review.
	SectionScopeSyncOnly SectionScope = "sync_only"
	// SectionScopeAsyncOnly includes the section only in asynchronous scoring.
	SectionScopeAsyncOnly SectionScope = "async_only"
)

// Includes reports whether the section belongs in the requested consumer.
func (s SectionScope) Includes(target ContextTarget) bool {
	switch s {
	case SectionScopeShared:
		return true
	case SectionScopeSyncOnly:
		return target == ContextTargetSync
	case SectionScopeAsyncOnly:
		return target == ContextTargetAsync
	default:
		return false
	}
}

// SectionInput carries the host inputs available while one Guardian context
// section is built.
type SectionInput struct {
	// Target is the consumer for which the host is collecting sections.
	Target ContextTarget
	// History is the parent conversation history available to this
	// contribution.
	History []eventmap.ResponseItem
}

// SectionContributor supplies one independently scoped section to Guardian
// context assembly.
//
// Implementations declare whether they apply to synchronous review,
// asynchronous scoring, or both; the registry filters contributors by scope
// before invoking them. Contributors distinguish sections that do not apply
// from required evidence that could not be collected.
type SectionContributor interface {
	// Scope reports the Guardian consumers that should receive this
	// contribution.
	Scope() SectionScope
	// Contribute builds the section from the host's current conversation
	// snapshot. It returns a nil section and nil error only when the section is
	// optional or does not apply; missing required evidence must return an
	// error so callers never review a partial context as though collection
	// succeeded.
	Contribute(input SectionInput) (*ContextSection, error)
}

// ErrMissingRequiredEvidence reports that a contributor could not collect the
// evidence a valid review context requires.
var ErrMissingRequiredEvidence = errors.New("missing required evidence")

// SectionError reports that a section could not provide the evidence needed for
// a valid review context.
type SectionError struct {
	// Section names the contributor whose required evidence was missing.
	Section string
}

// MissingRequiredEvidence returns the error a contributor reports when the
// evidence it requires is absent from the current input.
func MissingRequiredEvidence(section string) *SectionError {
	return &SectionError{Section: section}
}

// Error implements error.
func (e *SectionError) Error() string {
	if e == nil {
		return ErrMissingRequiredEvidence.Error()
	}
	return fmt.Sprintf("%s for section %s", ErrMissingRequiredEvidence, e.Section)
}

// Unwrap reports the shared missing-evidence sentinel for errors.Is.
func (e *SectionError) Unwrap() error { return ErrMissingRequiredEvidence }

// ContextSection is the ordered transcript evidence produced by one section
// contributor.
type ContextSection struct {
	// Items is structured evidence before consumer-specific selection and
	// rendering.
	Items []ConversationTranscriptEntry
}

// SectionRegistry is an ordered collection of independently scoped Guardian
// section contributors.
//
// Register contributors before serving reviews; Collect itself holds no
// mutable state and is safe for concurrent use.
type SectionRegistry struct {
	contributors []SectionContributor
}

// Register adds contributor to the end of the section collection order. A nil
// contributor is ignored.
func (r *SectionRegistry) Register(contributor SectionContributor) {
	if contributor == nil {
		return
	}
	r.contributors = append(r.contributors, contributor)
}

// Collect gathers applicable sections in their original registration order.
//
// It stops at the first error without returning any partial context; the host
// decides whether to fall back to synchronous review or deny approval.
func (r *SectionRegistry) Collect(input SectionInput) ([]ContextSection, error) {
	if r == nil {
		return nil, nil
	}
	var sections []ContextSection
	for _, contributor := range r.contributors {
		if !contributor.Scope().Includes(input.Target) {
			continue
		}
		section, err := contributor.Contribute(input)
		if err != nil {
			return nil, err
		}
		if section == nil {
			continue
		}
		sections = append(sections, *section)
	}
	return sections, nil
}
