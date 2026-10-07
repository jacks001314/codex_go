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
		FileName:  &fileName,
		Data:      []byte("secret"),
	}
	result := InlineUploadResult([]byte("secret"))

	if got, want := request.Debug(), `UploadRequest { thread_id: "thread-1", ephemeral: false, file_name: Some("image.png"), data: "<redacted>" }`; got != want {
		t.Fatalf("UploadRequest debug = %s, want %s", got, want)
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
			FileName:  &fileName,
			Data:      tc.data,
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
	request := UploadRequest{ThreadID: "thread-1", Ephemeral: true, Data: []byte("image")}
	if got, want := request.Debug(), `UploadRequest { thread_id: "thread-1", ephemeral: true, file_name: None, data: "<redacted>" }`; got != want {
		t.Fatalf("ephemeral debug = %s, want %s", got, want)
	}
	if got, want := FileUploadResult("file_1").Debug(), `File { file_id: "file_1" }`; got != want {
		t.Fatalf("file result debug = %s, want %s", got, want)
	}
}
