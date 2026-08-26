package appserver

import (
	"encoding/json"
	"reflect"
	"testing"

	"codex_go/tool"
)

func TestNodeReplReviewTextBlocksFiltersAndFallsBack(t *testing.T) {
	output := &tool.Output{Data: map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "first"},
			map[string]any{"type": "text", "text": "  "},
			map[string]any{"type": "image", "text": "ignored-image"},
			map[string]any{"type": "text", "text": "encrypted", "_meta": map[string]any{"codex/encryptedContent": true}},
			map[string]any{"type": "text", "text": "second"},
		},
	}}
	got := nodeReplReviewTextBlocks(output)
	if !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("nodeReplReviewTextBlocks = %#v", got)
	}

	structured := map[string]any{"rows": []any{1, 2, 3}}
	empty := &tool.Output{Data: map[string]any{
		"content":           []any{},
		"structuredContent": structured,
	}}
	fallback := nodeReplReviewTextBlocks(empty)
	if len(fallback) != 1 {
		t.Fatalf("structured fallback = %#v", fallback)
	}
	var decoded any
	if err := json.Unmarshal([]byte(fallback[0]), &decoded); err != nil {
		t.Fatalf("structured fallback is not valid JSON: %v", err)
	}

	if got := nodeReplReviewTextBlocks(nil); got != nil {
		t.Fatalf("nil output blocks = %#v", got)
	}
}

func TestNodeReplItemEncrypted(t *testing.T) {
	if nodeReplItemEncrypted(nil) {
		t.Fatal("nil item reported encrypted")
	}
	if !nodeReplItemEncrypted(map[string]any{"_meta": map[string]any{"codex/encryptedContent": true}}) {
		t.Fatal("encrypted item not detected")
	}
	if nodeReplItemEncrypted(map[string]any{"_meta": map[string]any{"codex/encryptedContent": false}}) {
		t.Fatal("plain item reported encrypted")
	}
}

func TestNodeReplReviewImageURLsFiltersAndNormalizes(t *testing.T) {
	output := &tool.Output{Data: map[string]any{
		"content": []any{
			map[string]any{"type": "image", "image_url": "data:image/png;base64,AAAA"},
			map[string]any{"type": "text", "text": "ignored"},
			map[string]any{"type": "image", "url": "data:image/png;base64,BBBB"},
			map[string]any{"type": "image", "image_url": "data:image/png;base64,SECRET", "_meta": map[string]any{"codex/encryptedContent": true}},
		},
	}}
	got := nodeReplReviewImageURLs(output)
	if !reflect.DeepEqual(got, []string{"data:image/png;base64,AAAA", "data:image/png;base64,BBBB"}) {
		t.Fatalf("nodeReplReviewImageURLs = %#v", got)
	}
	if got := nodeReplReviewImageURLs(nil); got != nil {
		t.Fatalf("nil output image urls = %#v", got)
	}
}

func TestNodeReplEvidenceStoreClearAndSnapshot(t *testing.T) {
	r := &RuntimeRouter{}
	evidence := r.nodeReplEvidenceForThread("thread-1")
	evidence.Record("js", "cell-1", "call-1", []string{"retained"})
	if r.guardianReviewNodeReplEvidence("thread-1", 0) == nil {
		t.Fatal("expected evidence snapshot")
	}
	r.clearNodeReplReviewEvidence("thread-1")
	if r.guardianReviewNodeReplEvidence("thread-1", 0) != nil {
		t.Fatal("cleared evidence should not produce a snapshot")
	}
}
func TestIsNodeReplBackedNamespaceRecognizesCuaRepl(t *testing.T) {
	for _, namespace := range []string{"node_repl", "cua_repl", "mcp__node_repl", "mcp__cua_repl"} {
		if !isNodeReplBackedNamespace(namespace) {
			t.Fatalf("namespace %q should be node-repl backed", namespace)
		}
	}
	for _, namespace := range []string{"exec", "git", "filesystem"} {
		if isNodeReplBackedNamespace(namespace) {
			t.Fatalf("namespace %q should not be node-repl backed", namespace)
		}
	}
}
