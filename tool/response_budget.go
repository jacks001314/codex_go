package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// InvocationSourceCodeMode marks a nested Code Mode tool call, mirroring Rust's
// ToolCallSource::CodeMode. Every other source (including the empty value and
// the plaintext-message source) is a direct call.
const InvocationSourceCodeMode = "code_mode"

// ResponseByteBudget mirrors Rust's `ToolCall::response_byte_budget`: a direct
// call is bounded by the host's effective text-output allowance, while a Code
// Mode call receives typed results without that truncation, so only the tool's
// own size limit applies. Callers must include serialization overhead when
// fitting a response to this budget.
//
// Rust always has a truncation policy; when the Go host did not supply one the
// tool's own limit governs, which is the same outcome whenever the model's
// allowance is at least as large as the tool limit (the common case).
func (i *Invocation) ResponseByteBudget(maxResponseBytes int) int {
	if maxResponseBytes < 0 {
		maxResponseBytes = 0
	}
	if i == nil || i.Source == InvocationSourceCodeMode || i.Truncation == nil {
		return maxResponseBytes
	}
	allowance := i.Truncation.WithSerializationAllowance()
	if budget := (&allowance).ByteBudget(); budget < maxResponseBytes {
		return budget
	}
	return maxResponseBytes
}

// IsCodeModeCall reports whether this invocation is a nested Code Mode call.
func (i *Invocation) IsCodeModeCall() bool {
	return i != nil && i.Source == InvocationSourceCodeMode
}

// DecodeStrictArguments decodes a function-call payload with Rust's
// `serde(deny_unknown_fields)` semantics: unknown fields and trailing JSON are
// rejected. Tools whose Rust argument structs deny unknown fields must decode
// through this helper instead of Invocation.DecodeArguments.
func (i *Invocation) DecodeStrictArguments(target any) error {
	if i == nil {
		return fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if target == nil {
		return fmt.Errorf("%w: target is nil", ErrToolInvalidCall)
	}
	raw := i.Payload.Arguments
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing JSON")
	}
	return nil
}
