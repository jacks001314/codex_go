package appserverdaemon

// ProcessIdentity is the untagged wire shape of Rust's ProcessIdentity
// (#45779). Only the fields for the current platform are populated.
type ProcessIdentity struct {
	BootID            string  `json:"bootId,omitempty"`
	StartTicks        *uint64 `json:"startTicks,omitempty"`
	UniqueID          *uint64 `json:"uniqueId,omitempty"`
	StartSeconds      *uint64 `json:"startSeconds,omitempty"`
	StartMicroseconds *uint64 `json:"startMicroseconds,omitempty"`
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}
