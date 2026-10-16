package context

import (
	"strings"
	"sync"
)

// GuardianImageRef identifies one Guardian image reference without its detail,
// mirroring the identity Rust compares in ComposedContext::retain_new_images
// (#54278, guardian-context/src/images.rs): an image is the same image no matter
// which detail the host selects for it.
type GuardianImageRef struct {
	// Source is the reference kind: GuardianImageSourceInline for an inline
	// image URL / data URL, GuardianImageSourceFile for a stored file id.
	Source string
	// Ref is the image URL, data URL, or file id that proves delivery.
	Ref string
}

const (
	// GuardianImageSourceInline marks an inline (data) URL reference, Rust's
	// ImageReference::Inline.
	GuardianImageSourceInline = "inline"
	// GuardianImageSourceFile marks a stored file reference, Rust's
	// ImageReference::File.
	GuardianImageSourceFile = "file"
)

// GuardianAdmittedImages is the admitted-history ledger for Guardian review
// images. Rust only accepts admitted history as proof of delivery ("Only
// admitted history proves delivery; images kept here are not committed yet."),
// so the ledger is filled from what the reviewer conversation actually received,
// never from the pending prompt (Rust #54278).
type GuardianAdmittedImages struct {
	mu   sync.Mutex
	refs map[GuardianImageRef]struct{}
}

// NewGuardianAdmittedImages returns an empty admitted-image ledger.
func NewGuardianAdmittedImages() *GuardianAdmittedImages {
	return &GuardianAdmittedImages{refs: map[GuardianImageRef]struct{}{}}
}

// Admit records references the reviewer conversation admitted. Empty references
// carry no identity and are ignored.
func (l *GuardianAdmittedImages) Admit(refs ...GuardianImageRef) {
	if l == nil || len(refs) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refs == nil {
		l.refs = map[GuardianImageRef]struct{}{}
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref.Ref) == "" {
			continue
		}
		l.refs[ref] = struct{}{}
	}
}

// Admitted reports whether the reference is already part of admitted history.
func (l *GuardianAdmittedImages) Admitted(ref GuardianImageRef) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.refs[ref]
	return ok
}

// AdmittedRefs returns a snapshot of the admitted references in ledger order.
func (l *GuardianAdmittedImages) AdmittedRefs() []GuardianImageRef {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	refs := make([]GuardianImageRef, 0, len(l.refs))
	for ref := range l.refs {
		refs = append(refs, ref)
	}
	return refs
}

// RetainNewImages mirrors Rust ComposedContext::retain_new_images (#54278): it
// returns the host-prepared candidates that admitted history does not already
// contain, comparing references only and ignoring image detail. The host supplies
// the reference its model will receive (for example after resizing) and may
// reject an image by returning false. The ledger is only scanned when at least
// one candidate needs checking, so an image-free continuation never touches
// retained history (Rust's LazyCell).
func (l *GuardianAdmittedImages) RetainNewImages(candidates []GuardianImageRef, prepare func(GuardianImageRef) (GuardianImageRef, bool)) []GuardianImageRef {
	if len(candidates) == 0 {
		return nil
	}
	return RetainNewGuardianImages(candidates, l.AdmittedRefs(), prepare)
}

// RetainNewGuardianImages is the pure core of retain_new_images: it deduplicates
// candidates against an already-materialized admitted history. A nil history
// means nothing has been admitted yet. Keeping this separate lets callers prove
// the image-free short circuit without consulting a ledger at all.
func RetainNewGuardianImages(candidates []GuardianImageRef, history []GuardianImageRef, prepare func(GuardianImageRef) (GuardianImageRef, bool)) []GuardianImageRef {
	if len(candidates) == 0 {
		return nil
	}
	var admitted map[GuardianImageRef]struct{}
	for _, ref := range history {
		if strings.TrimSpace(ref.Ref) == "" {
			continue
		}
		if admitted == nil {
			admitted = make(map[GuardianImageRef]struct{}, len(history))
		}
		admitted[ref] = struct{}{}
	}
	kept := make([]GuardianImageRef, 0, len(candidates))
	for _, candidate := range candidates {
		prepared := candidate
		if prepare != nil {
			resolved, ok := prepare(candidate)
			if !ok {
				continue
			}
			prepared = resolved
		}
		if strings.TrimSpace(prepared.Ref) == "" {
			continue
		}
		if _, ok := admitted[prepared]; ok {
			continue
		}
		kept = append(kept, prepared)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}
