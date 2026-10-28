package context

// Context fragment content-kind classifications shared by the fragments that
// produce them and the consumers that classify them, mirroring Rust #54316
// (`codex-rs/context-fragments/src/lib.rs` plus the per-fragment kinds in
// `core/src/context/{compaction_summary,memory,unsupported_media}.rs`).
//
// Producers annotate every content item through the persisted
// `internal_chat_message_metadata_passthrough.content_item_kinds` list
// (Rust `AnnotatedContent::kind`); consumers - authorization checks,
// forked-history filtering and the Guardian transcript filter - compare
// against these constants instead of repeating the strings.
const (
	// CompactionSummaryKind classifies a compaction summary produced by core and
	// filtered out of Guardian transcripts (Rust
	// `codex_context_fragments::COMPACTION_SUMMARY_KIND`, shared between core and
	// guardian-context).
	CompactionSummaryKind = "compaction.summary"
	// GuardianApprovedActionKind classifies the user's approval of one exact
	// previously denied Guardian action (Rust `GuardianApprovedAction`).
	GuardianApprovedActionKind = "guardian.approved_action"
	// SubagentNotificationKind classifies harness-authored child status
	// notifications (Rust `SubagentNotification`).
	SubagentNotificationKind = "multi_agent.subagent_notification"
	// MemoryReadInstructionsKind classifies the memory read-path instructions
	// fragment (Rust `MemoryContextFragment::READ_INSTRUCTIONS_KIND`).
	MemoryReadInstructionsKind = "memories.instructions"
	// MemoryExtractionEvidenceKind classifies the memory extraction-evidence
	// fragment (Rust `MemoryContextFragment::ExtractionEvidence`).
	MemoryExtractionEvidenceKind = "memories.extraction_evidence"
	// UnsupportedImageKind classifies the placeholder that replaced an image the
	// harness could not forward (Rust `UnsupportedMedia::IMAGE_KIND`).
	UnsupportedImageKind = "images.unsupported"
	// UnsupportedAudioKind classifies the placeholder that replaced audio the
	// harness could not forward (Rust `UnsupportedMedia::AUDIO_KIND`).
	UnsupportedAudioKind = "audio.unsupported"
	// ImagePreparationErrorKind classifies an image whose preparation failed
	// before the request (Rust `ImagePreparationError::KIND`).
	ImagePreparationErrorKind = "images.preparation_error"
	// ImageUnavailableKind classifies the placeholder that replaced a file image
	// confirmed missing at request time (Rust #57585 `images.unavailable`).
	ImageUnavailableKind = "images.unavailable"
	// UnknownContentKind is the classification a legacy item carries when the
	// harness did not annotate its content (Rust `"" | "unknown"`).
	UnknownContentKind = "unknown"
)
