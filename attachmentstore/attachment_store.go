// Package attachmentstore ports Rust's `codex-attachment-store` crate: the
// storage-neutral interfaces image preparation uses to hand attachments to a
// backend that decides whether they stay inline or become file references.
//
// Rust #51517 ("Pass thread persistence intent to attachment uploads") adds
// `UploadRequest::ephemeral`: attachment stores must be able to tell uploads
// from ephemeral threads, which intentionally skip durable persistence. The Go
// port carries the same field on the same request type so a durable store can
// honour it, and image preparation fills it from the turn's configuration.
//
// Structural differences from the Rust crate (recorded in update/plan_*):
//   - Rust's trait methods are async (`UploadFuture`/`ResolveFuture`); the Go
//     image-preparation pipeline is synchronous, so the interface methods are
//     synchronous too.
//   - Rust derives serde on the request/result types; Go has no consumer that
//     serializes them, so only the Debug renderings are ported (the manual
//     `Debug` impls that redact attachment bytes and file URLs).
package attachmentstore

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrorKind is the category of failure returned by a Store, mirroring Rust
// `AttachmentStoreErrorKind`. The string values match Rust's `Debug` rendering
// so logs keep the same vocabulary (`kind = ?error.kind()`).
type ErrorKind string

const (
	// ErrorKindNotFound means the requested attachment could not be resolved.
	ErrorKindNotFound ErrorKind = "NotFound"
	// ErrorKindInvalidAttachment means the attachment data or metadata is invalid.
	ErrorKindInvalidAttachment ErrorKind = "InvalidAttachment"
	// ErrorKindBackend means the backing store could not complete the operation.
	ErrorKindBackend ErrorKind = "Backend"
)

// StoreError is the error returned by a Store implementation, mirroring Rust
// `AttachmentStoreError`: a stable category plus a message, without exposing
// backend-specific error types.
type StoreError struct {
	Kind    ErrorKind
	Message string
}

// NewStoreError creates an error without exposing backend-specific error types.
func NewStoreError(kind ErrorKind, message string) *StoreError {
	return &StoreError{Kind: kind, Message: message}
}

// Error renders the message, mirroring Rust's `Display` impl for
// `AttachmentStoreError` (which forwards to the message).
func (e *StoreError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// KindOf reports the stable category of an error produced by a Store. It
// mirrors Rust's `AttachmentStoreError::kind()`.
func KindOf(err error) (ErrorKind, bool) {
	var storeErr *StoreError
	if errors.As(err, &storeErr) && storeErr != nil {
		return storeErr.Kind, true
	}
	return "", false
}

// Generated-image upload tags (Rust #54398, `ext/image-generation/src/tool.rs`):
// the attachment store sees `usage=image_gen` on generated images so backends can
// apply image-generation policy, while image preparation keeps an empty tag map.
const (
	// TagUsageKey is the tag key carrying the caller's usage label.
	TagUsageKey = "usage"
	// TagUsageImageGen marks an upload as a generated image.
	TagUsageImageGen = "image_gen"
)

// ImageGenerationUploadTags builds the tag set for a generated-image upload.
func ImageGenerationUploadTags() map[string]string {
	return map[string]string{TagUsageKey: TagUsageImageGen}
}

// UploadRequest is the attachment data supplied to Store.Upload, mirroring Rust
// `UploadRequest`.
type UploadRequest struct {
	// ThreadID is the thread receiving this attachment; storage backends may
	// use it for placement.
	ThreadID string
	// Ephemeral reports whether the originating thread intentionally skips
	// durable persistence (Rust #51517).
	Ephemeral bool
	// Content is the type-specific attachment data (Rust #62047).
	Content UploadContent
}

// UploadContentKind selects the typed content of an upload, mirroring Rust
// `UploadContent` (#62047).
type UploadContentKind string

const (
	// UploadContentImage is an image that may be shown to the model or kept
	// for the UI.
	UploadContentImage UploadContentKind = "image"
	// UploadContentToolAudio is audio produced by a tool. Persistent backends
	// must verify DeclaredMIMEType against the uploaded bytes.
	UploadContentToolAudio UploadContentKind = "tool_audio"
)

// UploadContent is the type-specific attachment content supplied to
// Store.Upload, mirroring Rust `UploadContent`. Image uploads keep their
// filename and policy tags; tool audio carries its declared media type
// explicitly instead of encoding it in tags.
type UploadContent struct {
	Kind UploadContentKind
	// FileName is the optional name associated with an image.
	FileName *string
	// Tags are caller-supplied labels image backends use for policy decisions
	// (Rust #54398). Generated images carry `usage=image_gen`; image
	// preparation passes an empty map.
	Tags map[string]string
	// DeclaredMIMEType is the IANA media type a persistent backend must verify
	// against the uploaded audio bytes.
	DeclaredMIMEType string
	// Data is the encoded attachment bytes.
	Data []byte
}

// ImageUploadContent builds an image upload, matching Rust's
// `UploadContent::Image` variant.
func ImageUploadContent(fileName *string, tags map[string]string, data []byte) UploadContent {
	return UploadContent{Kind: UploadContentImage, FileName: fileName, Tags: tags, Data: data}
}

// ToolAudioUploadContent builds a tool-audio upload, matching Rust's
// `UploadContent::ToolAudio` variant.
func ToolAudioUploadContent(declaredMIMEType string, data []byte) UploadContent {
	return UploadContent{Kind: UploadContentToolAudio, DeclaredMIMEType: declaredMIMEType, Data: data}
}

// Debug mirrors Rust's manual `Debug` impl for `UploadContent`, which redacts
// the payload for both variants.
func (c UploadContent) Debug() string {
	if c.Kind == UploadContentToolAudio {
		return fmt.Sprintf(
			"ToolAudio { declared_mime_type: %s, data: \"<redacted>\" }",
			rustDebugString(c.DeclaredMIMEType),
		)
	}
	return fmt.Sprintf(
		"Image { file_name: %s, tags: %s, data: \"<redacted>\" }",
		rustDebugOptionString(c.FileName),
		rustDebugTags(c.Tags),
	)
}

// String returns the Rust `Debug` rendering, so Go log output matches
// `tracing` output for the same request.
func (r UploadRequest) String() string { return r.Debug() }

// Debug mirrors Rust's manual `Debug` impl for `UploadRequest`, which forwards
// to the redacted `UploadContent` rendering:
// `UploadRequest { thread_id: "thread-1", ephemeral: false, content: Image { file_name: Some("image.png"), tags: {}, data: "<redacted>" } }`.
func (r UploadRequest) Debug() string {
	return fmt.Sprintf(
		"UploadRequest { thread_id: %s, ephemeral: %t, content: %s }",
		rustDebugString(r.ThreadID),
		r.Ephemeral,
		r.Content.Debug(),
	)
}

// GoString keeps `%#v` output redacted as well.
func (r UploadRequest) GoString() string { return r.Debug() }

// UploadResult is the representation callers should retain after uploading an
// attachment, mirroring Rust `UploadResult`. Exactly one of the two variants is
// populated: InlineBytes set means the attachment data remains inline, FileID
// set means the data is available through an external file reference.
type UploadResult struct {
	// InlineBytes are the attachment bytes the caller should keep inline
	// (Rust `UploadResult::Inline { bytes }`).
	InlineBytes []byte
	// FileID is the identifier assigned by the attachment store (Rust
	// `UploadResult::File { file_id }`).
	FileID string
}

// InlineUploadResult builds the inline variant.
func InlineUploadResult(bytes []byte) UploadResult {
	return UploadResult{InlineBytes: bytes}
}

// FileUploadResult builds the file-reference variant.
func FileUploadResult(fileID string) UploadResult {
	return UploadResult{FileID: fileID}
}

// IsFile reports whether the result is a file reference rather than inline data.
func (r UploadResult) IsFile() bool { return r.FileID != "" }

// String returns the Rust `Debug` rendering of the result.
func (r UploadResult) String() string { return r.Debug() }

// Debug mirrors Rust's manual `Debug` impl for `UploadResult`, which redacts
// the inline bytes.
func (r UploadResult) Debug() string {
	if r.IsFile() {
		return fmt.Sprintf("File { file_id: %s }", rustDebugString(r.FileID))
	}
	return `Inline { bytes: "<redacted>" }`
}

// GoString keeps `%#v` output redacted as well.
func (r UploadResult) GoString() string { return r.Debug() }

// ResolveRequest carries the parameters for Store.Resolve, mirroring Rust
// `ResolveRequest`.
type ResolveRequest struct {
	// FileID is the identifier assigned by the attachment store.
	FileID string
	// Tags are caller-supplied labels storage backends use for policy
	// decisions (Rust #54398 adds `ResolveRequest::tags`).
	Tags map[string]string
	// DownloadURLTTL, when non-nil, is the minimum remaining lifetime required
	// for the returned file URL. When nil, implementations must omit the file
	// URL and may return cached metadata.
	DownloadURLTTL *time.Duration
}

// AttachmentMetadata is the metadata used to validate and interpret an
// attachment, mirroring Rust `AttachmentMetadata`.
type AttachmentMetadata struct {
	FileName       *string                 `json:"file_name,omitempty"`
	Digest         *string                 `json:"digest,omitempty"`
	SizeBytes      *int64                  `json:"size_bytes,omitempty"`
	MimeType       *string                 `json:"mime_type,omitempty"`
	FormatSpecific *FormatSpecificMetadata `json:"format_specific,omitempty"`
	// FileURL is the URL for reading the attachment bytes when requested during
	// resolution. Implementations must omit it when ResolveRequest.DownloadURLTTL
	// is nil.
	FileURL *string `json:"file_url,omitempty"`
}

// String returns the Rust `Debug` rendering of the metadata.
func (m AttachmentMetadata) String() string { return m.Debug() }

// Debug mirrors Rust's manual `Debug` impl for `AttachmentMetadata`, which
// redacts the credential-bearing file URL.
func (m AttachmentMetadata) Debug() string {
	var fileURL any = nil
	if m.FileURL != nil {
		fileURL = "<redacted>"
	}
	return fmt.Sprintf(
		"AttachmentMetadata { file_name: %s, digest: %s, size_bytes: %s, mime_type: %s, format_specific: %s, file_url: %s }",
		rustDebugOptionString(m.FileName),
		rustDebugOptionString(m.Digest),
		rustDebugOptionInt64(m.SizeBytes),
		rustDebugOptionString(m.MimeType),
		m.FormatSpecific.Debug(),
		rustDebugOptionAny(fileURL),
	)
}

// GoString keeps `%#v` output redacted as well.
func (m AttachmentMetadata) GoString() string { return m.Debug() }

// FormatSpecificMetadata is metadata specific to an attachment format,
// mirroring Rust's tagged `FormatSpecificMetadata` enum (`kind: "image"` or
// `kind: "audio"`, the latter added by #59268).
type FormatSpecificMetadata struct {
	// Kind is the format tag (`"image"` or `"audio"`).
	Kind  string         `json:"kind"`
	Image *ImageMetadata `json:"-"`
	Audio *AudioMetadata `json:"-"`
}

// ImageFormatSpecificMetadata builds the image variant.
func ImageFormatSpecificMetadata(image *ImageMetadata) *FormatSpecificMetadata {
	if image == nil {
		image = &ImageMetadata{}
	}
	return &FormatSpecificMetadata{Kind: "image", Image: image}
}

// String returns the Rust `Debug` rendering of the format metadata.
func (f *FormatSpecificMetadata) String() string {
	if f == nil {
		return "None"
	}
	return f.Debug()
}

// Debug mirrors Rust's derived `Debug` for the tagged enum.
func (f *FormatSpecificMetadata) Debug() string {
	if f == nil {
		return "None"
	}
	if f.Kind == "image" {
		return fmt.Sprintf("Image(%s)", f.ImageOrZero().Debug())
	}
	if f.Kind == "audio" {
		return fmt.Sprintf("Audio(%s)", f.AudioOrZero().Debug())
	}
	if f.Kind == "audio" {
		return fmt.Sprintf("Audio(%s)", f.AudioOrZero().Debug())
	}
	return fmt.Sprintf("FormatSpecific(kind: %s)", rustDebugString(f.Kind))
}

// ImageOrZero returns the image metadata, defaulting to an empty value.
func (f *FormatSpecificMetadata) ImageOrZero() *ImageMetadata {
	if f == nil || f.Image == nil {
		return &ImageMetadata{}
	}
	return f.Image
}

// AudioFormatSpecificMetadata builds the audio variant.
func AudioFormatSpecificMetadata(audio *AudioMetadata) *FormatSpecificMetadata {
	if audio == nil {
		audio = &AudioMetadata{}
	}
	return &FormatSpecificMetadata{Kind: "audio", Audio: audio}
}

// AudioOrZero returns the audio metadata, defaulting to an empty value.
func (f *FormatSpecificMetadata) AudioOrZero() *AudioMetadata {
	if f == nil || f.Audio == nil {
		return &AudioMetadata{}
	}
	return f.Audio
}

// AudioMetadata is metadata specific to an audio attachment, mirroring Rust
// `AudioMetadata` (#59268). DurationMs carries the store's measured duration
// when the backend knows it.
type AudioMetadata struct {
	DurationMs *uint64 `json:"duration_ms,omitempty"`
}

// String returns the Rust `Debug` rendering of the metadata.
func (m AudioMetadata) String() string { return m.Debug() }

// Debug mirrors Rust's derived `Debug` for `AudioMetadata`.
func (m AudioMetadata) Debug() string {
	duration := "None"
	if m.DurationMs != nil {
		duration = fmt.Sprintf("Some(%d)", *m.DurationMs)
	}
	return fmt.Sprintf("AudioMetadata { duration_ms: %s }", duration)
}

// ImageMetadata is metadata specific to an image attachment, mirroring Rust
// `ImageMetadata`.
type ImageMetadata struct {
	Width  *uint32 `json:"width,omitempty"`
	Height *uint32 `json:"height,omitempty"`
}

// String returns the Rust `Debug` rendering of the image metadata.
func (i ImageMetadata) String() string { return i.Debug() }

// Debug mirrors Rust's derived `Debug` for `ImageMetadata`.
func (i ImageMetadata) Debug() string {
	return fmt.Sprintf(
		"ImageMetadata { width: %s, height: %s }",
		rustDebugOptionUint32(i.Width),
		rustDebugOptionUint32(i.Height),
	)
}

// Store uploads attachments and resolves metadata for stored files, mirroring
// Rust's `AttachmentStore` trait. Implementations choose whether uploaded
// attachments remain inline or become file references.
type Store interface {
	// Upload stores an attachment and returns the representation callers
	// should retain.
	Upload(request UploadRequest) (UploadResult, error)
	// Resolve returns metadata, and when requested a fresh URL, for a stored
	// attachment.
	Resolve(request ResolveRequest) (AttachmentMetadata, error)
}

// ResolvableStore is a Store that can confirm whether a file reference is
// still available (Rust #57585 `AttachmentStore::is_resolvable`). Returning
// false asserts the file is confirmed missing; backend and access errors must
// keep the reference, so implementations report true when unsure.
type ResolvableStore interface {
	Store
	IsResolvable(fileID string) bool
}

// IsResolvable reports whether fileID should stay in a model request. Stores
// that do not implement ResolvableStore - including InlineStore and stores that
// only know provider-owned IDs - retain every reference, matching Rust's
// default trait method.
func IsResolvable(store Store, fileID string) bool {
	if store == nil {
		return true
	}
	if checker, ok := store.(ResolvableStore); ok {
		return checker.IsResolvable(fileID)
	}
	return true
}

// InlineStore leaves attachments inline instead of storing them, mirroring Rust
// `InlineAttachmentStore`. It is the default store for every host.
type InlineStore struct{}

// Upload returns the request bytes unchanged, mirroring Rust's
// `InlineAttachmentStore::upload`.
func (InlineStore) Upload(request UploadRequest) (UploadResult, error) {
	return InlineUploadResult(request.Content.Data), nil
}

// Resolve always fails with ErrorKindNotFound: the inline store never uploads
// file references, mirroring Rust's `InlineAttachmentStore::resolve`.
func (InlineStore) Resolve(request ResolveRequest) (AttachmentMetadata, error) {
	return AttachmentMetadata{}, NewStoreError(
		ErrorKindNotFound,
		fmt.Sprintf("attachment `%s` was not found", request.FileID),
	)
}

// StoreOrDefault returns the store, substituting the inline store when none is
// configured. It mirrors Rust's `ThreadManager::passthrough_image_store()`
// default, which app-server hosts install when they have no durable backend.
func StoreOrDefault(store Store) Store {
	if store == nil {
		return InlineStore{}
	}
	return store
}

// rustDebugString renders a Go string the way Rust's `Debug` impl for `str`
// does, so ported assertions can compare against Rust's expected output
// verbatim.
// rustDebugTags renders a tag map the way Rust's derived `HashMap` `Debug` impl
// does: `{}` when empty, otherwise `{"key": "value", ...}`. Rust's iteration
// order is unspecified, so Go sorts the keys to keep the rendering stable.
func rustDebugTags(tags map[string]string) string {
	if len(tags) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", rustDebugString(key), rustDebugString(tags[key])))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func rustDebugString(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		case 0:
			builder.WriteString(`\0`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&builder, `\u{%x}`, r)
				continue
			}
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

func rustDebugOptionString(value *string) string {
	if value == nil {
		return "None"
	}
	return "Some(" + rustDebugString(*value) + ")"
}

func rustDebugOptionInt64(value *int64) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%d)", *value)
}

func rustDebugOptionUint32(value *uint32) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%d)", *value)
}

func rustDebugOptionAny(value any) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%v)", value)
}
