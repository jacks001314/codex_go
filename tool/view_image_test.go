package tool

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewImageSpecMatchesRustSource(t *testing.T) {
	spec := NewViewImageHandler(ViewImageOptions{CanRequestOriginalDetail: true, IncludeEnvironmentID: true}).Spec()
	if spec.Name.Key() != "view_image" || spec.Description != "View a local image file from the filesystem when visual inspection is needed. Use this for images already available on disk." {
		t.Fatalf("view_image spec = %#v", spec)
	}
	properties := spec.InputSchema["properties"].(map[string]any)
	if properties["path"] == nil || properties["detail"] == nil || properties["environment_id"] == nil {
		t.Fatalf("view_image properties = %#v", properties)
	}
	if spec.OutputSchema["additionalProperties"] != false {
		t.Fatalf("view_image output schema = %#v", spec.OutputSchema)
	}
}

func TestViewImageReadsRelativePathAndReturnsRustWireShape(t *testing.T) {
	dir := t.TempDir()
	want := []byte("image-bytes")
	if err := os.WriteFile(filepath.Join(dir, "sample.png"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewViewImageHandler(ViewImageOptions{CWD: dir, CanRequestOriginalDetail: true})
	out, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"path":"sample.png","detail":"original"}`}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Data["detail"] != "original" || !strings.HasPrefix(out.Data["image_url"].(string), "data:application/octet-stream;base64,") {
		t.Fatalf("view_image output = %#v", out)
	}
	encoded := strings.TrimPrefix(out.Data["image_url"].(string), "data:application/octet-stream;base64,")
	got, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(got) != string(want) {
		t.Fatalf("decoded image = %q, %v", got, err)
	}
}

func TestViewImageRejectsUnsupportedDetail(t *testing.T) {
	_, err := NewViewImageHandler(ViewImageOptions{}).Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"path":"x","detail":"low"}`}})
	if err == nil || !strings.Contains(err.Error(), "only supports `high` or `original`") {
		t.Fatalf("Execute(low) error = %v", err)
	}
}
