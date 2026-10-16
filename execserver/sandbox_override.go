package execserver

import "fmt"

// SandboxOverride mirrors Rust `codex_protocol::sandbox::SandboxOverride`
// (#51786 `Preserve sandbox overrides across execution requests`): the
// controller's decision that overrides normal sandbox selection for one command.
//
// The value travels on the file system sandbox context
// (`FileSystemSandboxContext.SandboxOverride`, wire key `sandboxOverride`) for
// observation only: it never widens the permissions the request carries and
// never changes the sandbox command. Rust omits `NoOverride` when serializing
// and defaults a missing field to `NoOverride`, so the zero value is the
// no-override value ("omitempty" drops it from the wire form) and an empty wire
// value means "no override", the same legacy default WindowsSandboxLevel keeps.
type SandboxOverride string

const (
	// SandboxOverrideNone is Rust's `SandboxOverride::NoOverride`.
	SandboxOverrideNone SandboxOverride = ""
	// SandboxOverrideEscalatedWithRestrictions is Rust's
	// `SandboxOverride::EscalatedSandboxWithRestrictions` (serde camelCase):
	// broaden approved filesystem access while retaining denied-read
	// enforcement.
	SandboxOverrideEscalatedWithRestrictions SandboxOverride = "escalatedSandboxWithRestrictions"
	// SandboxOverrideBypassFirstAttempt is Rust's
	// `SandboxOverride::BypassSandboxFirstAttempt`: bypass the sandbox on the
	// first attempt.
	SandboxOverrideBypassFirstAttempt SandboxOverride = "bypassSandboxFirstAttempt"
)

// IsNoOverride mirrors Rust's `SandboxOverride::is_no_override`, which serde
// uses as `skip_serializing_if` so the field disappears from the wire form.
func (o SandboxOverride) IsNoOverride() bool { return o == SandboxOverrideNone }

// Validate rejects an unparsable `sandboxOverride` the way Rust's enum
// deserialization does, with the same serde wording the sibling
// WindowsSandboxLevel parser uses, so the caller answers with invalid params
// instead of guessing.
func (o SandboxOverride) Validate() error {
	switch o {
	case SandboxOverrideNone, SandboxOverrideEscalatedWithRestrictions, SandboxOverrideBypassFirstAttempt:
		return nil
	default:
		return fmt.Errorf("unknown variant `%s`, expected one of `noOverride`, `escalatedSandboxWithRestrictions`, `bypassSandboxFirstAttempt`", o)
	}
}
