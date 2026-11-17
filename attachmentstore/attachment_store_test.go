package attachmentstore

import (
	"strings"
	"testing"
)

// TestAttachmentDebugOutputRedactsBytesLikeRust ports Rust
// `attachment_debug_output_redacts_bytes` (codex-rs/attachment-store/src/lib_tests.rs).
func TestAttachmentDebugOutputRedactsBytesLikeRust(t *testing.T) {
	fileName := "image.png"
	request := UploadRequest{
		ThreadID:  "thread-1",
		Ephemeral: false,
		Content:   ImageUploadContent(&fileName, nil, []byte("secret")),
	}
	toolAudio := ToolAudioUploadContent("audio/wav", []byte("secret"))
	result := InlineUploadResult([]byte("secret"))

	if got, want := request.Debug(), `UploadRequest { thread_id: "thread-1", ephemeral: false, content: Image { file_name: Some("image.png"), tags: {}, data: "<redacted>" } }`; got != want {
		t.Fatalf("UploadRequest debug = %s, want %s", got, want)
	}
	if got, want := toolAudio.Debug(), `ToolAudio { declared_mime_type: "audio/wav", data: "<redacted>" }`; got != want {
		t.Fatalf("UploadContent debug = %s, want %s", got, want)
	}
	if got, want := result.Debug(), `Inline { bytes: "<redacted>" }`; got != want {
		t.Fatalf("UploadResult debug = %s, want %s", got, want)
	}
}

// TestAttachmentMetadataDebugOutputRedactsFileURLLikeRust ports Rust
// `attachment_metadata_debug_output_redacts_file_url`.
func TestAttachmentMetadataDebugOutputRedactsFileURLLikeRust(t *testing.T) {
	fileURL := "https://attachments.test/file?signed=secret"
	debug := (AttachmentMetadata{FileURL: &fileURL}).Debug()

	if strings.Contains(debug, "signed=secret") {
		t.Fatalf("metadata debug leaked the signed url: %s", debug)
	}
	if !strings.Contains(debug, "<redacted>") {
		t.Fatalf("metadata debug did not redact the file url: %s", debug)
	}
}

// TestInlineStorePreservesImageBytesLikeRust ports Rust
// `inline_store_preserves_image_bytes`: the inline store returns PNG and JPEG
// bytes unchanged.
func TestInlineStorePreservesImageBytesLikeRust(t *testing.T) {
	cases := []struct {
		fileName string
		data     []byte
	}{
		{"image.png", []byte("\x89PNG\r\n\x1a\n")},
		{"image.jpg", []byte("\xff\xd8\xff\xe0JFIF\x00\xff\xd9")},
	}
	for _, tc := range cases {
		fileName := tc.fileName
		result, err := InlineStore{}.Upload(UploadRequest{
			ThreadID:  "thread-1",
			Ephemeral: false,
			Content:   ImageUploadContent(&fileName, nil, tc.data),
		})
		if err != nil {
			t.Fatalf("inline attachment: %v", err)
		}
		if result.IsFile() {
			t.Fatalf("inline store returned a file reference: %v", result)
		}
		if string(result.InlineBytes) != string(tc.data) {
			t.Fatalf("inline bytes = %q, want %q", result.InlineBytes, tc.data)
		}
	}
}

// TestInlineStorePreservesToolAudioBytesLikeRust ports Rust
// `inline_store_preserves_tool_audio_bytes`: the inline store returns the clip
// without interpreting the declared media type.
func TestInlineStorePreservesToolAudioBytesLikeRust(t *testing.T) {
	data := []byte("RIFF\x04\x00\x00\x00WAVE")
	result, err := InlineStore{}.Upload(UploadRequest{
		ThreadID: "thread-1",
		Content:  ToolAudioUploadContent("audio/wav", data),
	})
	if err != nil {
		t.Fatalf("inline tool audio: %v", err)
	}
	if result.IsFile() || string(result.InlineBytes) != string(data) {
		t.Fatalf("inline result = %v, want the uploaded bytes", result)
	}
}

// TestInlineStoreCannotResolveFileReferencesLikeRust ports Rust
// `inline_store_cannot_resolve_file_references`.
func TestInlineStoreCannotResolveFileReferencesLikeRust(t *testing.T) {
	_, err := InlineStore{}.Resolve(ResolveRequest{FileID: "file_123"})
	if err == nil {
		t.Fatal("inline store cannot resolve file references")
	}
	kind, ok := KindOf(err)
	if !ok {
		t.Fatalf("resolve error is not a StoreError: %v", err)
	}
	if kind != ErrorKindNotFound {
		t.Fatalf("resolve error kind = %s, want %s", kind, ErrorKindNotFound)
	}
	if got, want := err.Error(), "attachment `file_123` was not found"; got != want {
		t.Fatalf("resolve error = %q, want %q", got, want)
	}
}

// TestEphemeralIntentTravelsOnUploadRequestLikeRust freezes the field the Rust
// PR #51517 added: the request carries the originating thread's persistence
// intent next to the thread id.
func TestEphemeralIntentTravelsOnUploadRequestLikeRust(t *testing.T) {
	request := UploadRequest{ThreadID: "thread-1", Ephemeral: true, Content: ImageUploadContent(nil, nil, []byte("image"))}
	if got, want := request.Debug(), `UploadRequest { thread_id: "thread-1", ephemeral: true, content: Image { file_name: None, tags: {}, data: "<redacted>" } }`; got != want {
		t.Fatalf("ephemeral debug = %s, want %s", got, want)
	}
	if got, want := FileUploadResult("file_1").Debug(), `File { file_id: "file_1" }`; got != want {
		t.Fatalf("file result debug = %s, want %s", got, want)
	}
}

// TestUploadRequestCarriesPolicyTagsLikeRust ports the Rust #54398 field
// `UploadRequest::tags` (`codex-rs/attachment-store/src/lib.rs`), including the
// `tags: {}` / `tags: {"usage": "image_gen"}` renderings the manual `Debug` impl
// produces in `attachment_debug_output_redacts_bytes`.
func TestUploadRequestCarriesPolicyTagsLikeRust(t *testing.T) {
	empty := UploadRequest{ThreadID: "thread-1", Content: ImageUploadContent(nil, nil, []byte("secret"))}
	if got, want := empty.Debug(), `UploadRequest { thread_id: "thread-1", ephemeral: false, content: Image { file_name: None, tags: {}, data: "<redacted>" } }`; got != want {
		t.Fatalf("empty tags debug = %s, want %s", got, want)
	}
	tagged := UploadRequest{ThreadID: "thread-1", Content: ImageUploadContent(nil, ImageGenerationUploadTags(), []byte("secret"))}
	if got, want := tagged.Debug(), `UploadRequest { thread_id: "thread-1", ephemeral: false, content: Image { file_name: None, tags: {"usage": "image_gen"}, data: "<redacted>" } }`; got != want {
		t.Fatalf("generated-image tags debug = %s, want %s", got, want)
	}
	if got := ImageGenerationUploadTags()[TagUsageKey]; got != TagUsageImageGen {
		t.Fatalf("usage tag = %q, want %q", got, TagUsageImageGen)
	}
	resolve := ResolveRequest{FileID: "file_1", Tags: ImageGenerationUploadTags()}
	if len(resolve.Tags) != 1 || resolve.Tags["usage"] != "image_gen" {
		t.Fatalf("resolve tags = %#v", resolve.Tags)
	}
}
