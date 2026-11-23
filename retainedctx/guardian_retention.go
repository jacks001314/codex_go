package retainedctx

// GuardianRetentionPolicy mirrors Rust's protocol GuardianRetentionPolicy
// (#62827): the recorder limits resolved from the Sync Elastic retained
// instruction entry budget, in storage bytes. Every field is optional so a
// Fixed-budget snapshot and older rollouts omit it.
type GuardianRetentionPolicy struct {
	// AssistantRecordBytes bounds one retained assistant record. Absent means
	// the fixed 16 KiB record limit applies.
	AssistantRecordBytes *int `json:"assistant_record_bytes,omitempty"`
}

// Equal reports whether two policies resolve to the same recorder limits.
func (p *GuardianRetentionPolicy) Equal(other *GuardianRetentionPolicy) bool {
	if p == nil || other == nil {
		return p == nil && other == nil
	}
	return intPtrEqual(p.AssistantRecordBytes, other.AssistantRecordBytes)
}

func intPtrEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// Clone returns an independent copy.
func (p *GuardianRetentionPolicy) Clone() *GuardianRetentionPolicy {
	if p == nil {
		return nil
	}
	cloned := *p
	if p.AssistantRecordBytes != nil {
		value := *p.AssistantRecordBytes
		cloned.AssistantRecordBytes = &value
	}
	return &cloned
}
