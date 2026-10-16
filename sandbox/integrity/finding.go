package integrity

// Finding mirrors Rust `sandbox_integrity::IntegrityFinding`
// (sandbox_integrity/finding.rs:7-12): a containment dependency together with
// the effective write grants responsible for the finding.
type Finding struct {
	// Target is the dependency the caller asked about, exactly as it was
	// requested (Rust `IntegrityFinding::target`).
	Target string
	// WriteRoots are the writable roots that will let an agent modify the
	// resolved contents (Rust `IntegrityFinding::write_roots`).
	WriteRoots []string
	// Details is the evidence for the kind of weakness detected.
	Details FindingDetails
}

// FindingDetails mirrors Rust `sandbox_integrity::IntegrityFindingDetails`
// (sandbox_integrity/finding.rs:15-18). Upstream models it as an enum with a
// single `Contents { resolved_target }` variant today; Kind names the variant so
// a future variant cannot be mistaken for a well-formed check.
type FindingDetails struct {
	// ResolvedTarget is Target with symlinks resolved: the object whose contents
	// the policy would let an agent rewrite.
	ResolvedTarget string
}

// FindingKindContents is the Kind of the `Contents { resolved_target }` variant.
const FindingKindContents = "contents"

// Kind reports which IntegrityFindingDetails variant this is
// (sandbox_integrity/finding.rs:15-18).
func (d FindingDetails) Kind() string { return FindingKindContents }
