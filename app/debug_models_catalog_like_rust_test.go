package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
)

// TestDebugModelsPrintsBundledCatalogLikeRust pins `debug models` to the raw
// bundled catalog: Rust writes bundled_models_response() straight to stdout,
// so the snapshot bytes must reach the writer unchanged.
func TestDebugModelsPrintsBundledCatalogLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "models"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("debug models returned error: %v", err)
	}
	want := model.BundledModelsCatalogJSON()
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Fatalf("debug models wrote %d bytes, want the %d-byte bundled catalog", stdout.Len(), len(want))
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatal("debug models output is not valid JSON")
	}
}
