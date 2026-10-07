package context

import "strings"

// ContentFilterGuidanceDefault is Rust prompts/src/model_messages.rs
// CONTENT_FILTER_GUIDANCE (Rust #49119, upstream 8bd5a136ff): the bundled
// recovery guidance appended after a response is blocked by the content filter
// when the model catalog provides no usable override.
const ContentFilterGuidanceDefault = "Your previous response was blocked by a content filter. Do not treat this as a transient failure or try to reproduce or work around the blocked content through repeated attempts, altered formatting, splitting, encoding, tools, subagents, or later wakes. Briefly explain the limitation and offer a permitted alternative. Continue unrelated authorized work."

// MaxContentFilterGuidanceBytes is Rust
// prompts/src/model_messages.rs::MAX_CONTENT_FILTER_GUIDANCE_BYTES (Rust #49119):
// a catalog override larger than this many UTF-8 bytes is ignored, never
// truncated.
const MaxContentFilterGuidanceBytes = 512

const (
	contentFilterGuidanceOpenTag  = "<content_filter_guidance>"
	contentFilterGuidanceCloseTag = "</content_filter_guidance>"
	// ContentFilterGuidanceKind is the stable classification of the rendered
	// guidance (Rust ContentFilterGuidance::content_kind,
	// `ContentItemKind("generic.content_filter_guidance")`).
	ContentFilterGuidanceKind = "generic.content_filter_guidance"
)

// ResolveContentFilterGuidance mirrors Rust
// ResolvedModelMessages::content_filter_guidance (Rust #49119): a catalog value
// is used only when it is present, not blank, and at most
// MaxContentFilterGuidanceBytes UTF-8 bytes; missing, null, blank and oversized
// values all fall back to the bundled guidance. The bound filters, it does not
// truncate the instructions.
func ResolveContentFilterGuidance(catalog *string) string {
	if catalog == nil {
		return ContentFilterGuidanceDefault
	}
	text := *catalog
	if strings.TrimSpace(text) == "" || len(text) > MaxContentFilterGuidanceBytes {
		return ContentFilterGuidanceDefault
	}
	return text
}

// NewContentFilterGuidance builds the developer fragment carrying resolved
// content-filter recovery guidance (Rust #49119
// core/src/context/content_filter_guidance.rs). The body keeps Rust's
// surrounding newlines, so the rendered content is
// `<content_filter_guidance>\n{text}\n</content_filter_guidance>`.
func NewContentFilterGuidance(text string) *SimpleFragment {
	return NewSimpleFragmentWithKind(
		RoleDeveloper,
		contentFilterGuidanceOpenTag,
		contentFilterGuidanceCloseTag,
		"\n"+text+"\n",
		ContentFilterGuidanceKind,
	)
}
