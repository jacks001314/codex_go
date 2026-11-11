package codemode

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

// TestCodeModeContentForwardsMixedMCPBlocksLikeRust mirrors Rust #60665:
// content(iterable) preserves block order and types, resource blocks become
// text, and image metadata selects the image detail.
func TestCodeModeContentForwardsMixedMCPBlocksLikeRust(t *testing.T) {
	shortPayload := base64.StdEncoding.EncodeToString(pcm16WAV(24000, 1))
	source := `
		content([
			{type:"text", text:"first"},
			{type:"image", data:"aW1hZ2U=", mimeType:"image/png", _meta:{"codex/imageDetail":"original"}},
			{type:"audio", data:"` + shortPayload + `", mimeType:"audio/wav"},
			{type:"resource_link", uri:"file:///tmp/a.txt", name:"a"},
			{type:"resource", resource:{uri:"file:///tmp/b.txt", text:"embedded"}},
			{type:"text", text:"last"},
		]);
	`
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: source})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	original := ImageDetailOriginal
	want := []ContentItem{
		InputText("first"),
		InputImage("data:image/png;base64,aW1hZ2U=", &original),
		InputText(shortAudioOmissionText),
		InputText(`{"name":"a","type":"resource_link","uri":"file:///tmp/a.txt"}`),
		InputText(`{"resource":{"text":"embedded","uri":"file:///tmp/b.txt"},"type":"resource"}`),
		InputText("last"),
	}
	if !reflect.DeepEqual(result.ContentItems, want) {
		t.Fatalf("items = %#v, want %#v", result.ContentItems, want)
	}
}

// TestCodeModeContentClosesIteratorOnRejectedBlockLikeRust verifies the
// JavaScript wrapper reuses the iterator method and lets for-of close the
// iterator when the host rejects a block.
func TestCodeModeContentClosesIteratorOnRejectedBlockLikeRust(t *testing.T) {
	source := `
		let closed = false;
		const iterable = {
			[Symbol.iterator]() {
				let index = 0;
				return {
					next() {
						if (index++ === 0) return {value:{type:"text", text:"kept"}, done:false};
						return {value:{type:"unknown"}, done:false};
					},
					return() {
						closed = true;
						return {done:true};
					},
				};
			},
		};
		try { content(iterable); } catch (error) { text(String(closed)); }
	`
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: source})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []ContentItem{InputText("kept"), InputText("true")}
	if !reflect.DeepEqual(result.ContentItems, want) {
		t.Fatalf("items = %#v, want %#v", result.ContentItems, want)
	}
	_, err = NewSobekEngine().Execute(context.Background(), EngineRequest{Source: `content([{type:"text", text:"kept"},{type:"unknown"}])`})
	if err == nil || !strings.Contains(err.Error(), contentHelperExpectsMessage) {
		t.Fatalf("uncaught rejection error = %v, want %q", err, contentHelperExpectsMessage)
	}
}

// TestCodeModeContentRejectsInvalidAndEncryptedBlocksLikeRust covers the
// strict single-block path and encrypted MCP text.
func TestCodeModeContentRejectsInvalidAndEncryptedBlocksLikeRust(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "missing argument", source: `content()`, want: contentHelperExpectsMessage},
		{name: "string", source: `content("text")`, want: contentHelperExpectsMessage},
		{name: "array item", source: `content([[]])`, want: contentHelperExpectsMessage},
		{name: "unknown type", source: `content({type:"unknown"})`, want: contentHelperExpectsMessage},
		{name: "text without payload", source: `content({type:"text"})`, want: contentHelperExpectsMessage},
		{name: "image without data", source: `content({type:"image", mimeType:"image/png"})`, want: contentHelperExpectsMessage},
		{name: "encrypted text", source: `content({type:"text", text:"secret", _meta:{"codex/encryptedContent":true}})`, want: contentEncryptedMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: test.source})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("source %s error = %v, want %q", test.source, err, test.want)
			}
		})
	}
}

// TestCodeModeContentKeepsDefaultImageDetailLikeRust verifies the high default
// and that the content helper does not keep emitting after exit().
func TestCodeModeContentKeepsDefaultImageDetailLikeRust(t *testing.T) {
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: `
		content({type:"image", data:"data:image/png;base64,AAAA"});
		content({type:"image", data:"BBBB", mime_type:"image/jpeg", _meta:{"codex/imageDetail":"bogus"}});
		exit();
		content({type:"text", text:"after exit"});
	`})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	high := DefaultImageDetail
	want := []ContentItem{
		InputImage("data:image/png;base64,AAAA", &high),
		InputImage("data:image/jpeg;base64,BBBB", &high),
	}
	if !reflect.DeepEqual(result.ContentItems, want) {
		t.Fatalf("items = %#v, want %#v", result.ContentItems, want)
	}
}
