package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Rust's view_image_spec::create_view_image_tool drops the `detail` argument and
// the output `detail` hint when the unified image budget hides detail values,
// and view_image then keeps original detail for every call.
func TestViewImageUnifiedImageBudgetHidesDetailLikeRust(t *testing.T) {
	spec := NewViewImageHandler(ViewImageOptions{CanRequestOriginalDetail: true, UnifiedImageBudget: true}).Spec()
	properties := spec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["detail"]; ok {
		t.Fatalf("unified view_image input schema still advertises detail: %#v", properties)
	}
	if _, ok := properties["environment_id"]; ok {
		t.Fatalf("detail-less schema gained an unexpected property: %#v", properties)
	}
	outputProperties := spec.OutputSchema["properties"].(map[string]any)
	if _, ok := outputProperties["detail"]; ok {
		t.Fatalf("unified view_image output schema still advertises detail: %#v", outputProperties)
	}
	if required := spec.OutputSchema["required"].([]string); len(required) != 1 || required[0] != "image_url" {
		t.Fatalf("unified view_image required = %#v", spec.OutputSchema["required"])
	}

	dir := t.TempDir()
	data, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.png"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := NewViewImageHandler(ViewImageOptions{CWD: dir, UnifiedImageBudget: true}).Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"path":"sample.png","detail":"high"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Rust keeps original detail whenever the unified budget is active, whatever
	// hint the call carried, while the code-mode result stops reporting it.
	if detail, ok := out.Data["detail"]; ok {
		t.Fatalf("unified view_image result reports detail = %#v", detail)
	}
	items, ok := out.Data["content_items"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["detail"] != "original" {
		t.Fatalf("unified view_image content items = %#v", out.Data["content_items"])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out.Body), &body); err != nil {
		t.Fatalf("body = %q, %v", out.Body, err)
	}
	if _, ok := body["detail"]; ok {
		t.Fatalf("unified view_image result still reports detail: %s", out.Body)
	}

	legacy, err := NewViewImageHandler(ViewImageOptions{CWD: dir, CanRequestOriginalDetail: true}).Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"path":"sample.png"}`},
	})
	if err != nil {
		t.Fatalf("legacy Execute() error = %v", err)
	}
	if legacy.Data["detail"] != "high" {
		t.Fatalf("legacy view_image detail = %#v, want high", legacy.Data["detail"])
	}
}
