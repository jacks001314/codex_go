package parity

import (
	"path/filepath"
	"testing"

	"codex_go/turn"
)

// TestRustImagegenDescriptionMatchesGo is the djalign static-layer blob check
// for the image-generation tool description: Rust embeds
// ext/image-generation/imagegen_description.md via include_str! and passes it
// verbatim as the model-visible tool description
// (ext/image-generation/src/tool.rs `description: IMAGEGEN_DESCRIPTION`). Go
// vendors the same text in turn/image_generation.go. The two must match
// byte-for-byte (trailing newline included), so an upstream edit such as #45544
// ("Avoid printing the full result or its base64 image data ...") breaks this
// contract instead of silently drifting.
func TestRustImagegenDescriptionMatchesGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	// Read the blob from the checkout's object store: the working tree may carry
	// CRLF line endings on Windows, while Rust embeds the LF blob.
	blob := gitOutput(t, rustRepo, "show", "HEAD:codex-rs/ext/image-generation/imagegen_description.md")
	got := turn.NewImageGenerationHandler(nil).Spec().Description
	if got != string(blob) {
		t.Fatalf("image-generation description differs from Rust imagegen_description.md:\n--- go ---\n%s\n--- rust ---\n%s", got, blob)
	}
}
