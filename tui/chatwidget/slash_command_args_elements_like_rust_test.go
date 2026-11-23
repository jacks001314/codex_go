package chatwidget

import (
	"reflect"
	"testing"

	"codex_go/turn"
)

// Rust #62624: one helper maps full-text element ranges onto command-argument
// ranges for both slash submission and dispatch.
func TestSlashCommandArgsElementsMatchRustCore(t *testing.T) {
	rest := "inspect @file"
	offset := len("/review ")
	first := "inspect"
	second := "@file"
	placeholder := "mention"
	elements := []turn.TextElement{
		{ByteRange: turn.ByteRange{Start: 0, End: 7}},
		{ByteRange: turn.ByteRange{Start: uint(offset), End: uint(offset + len(first))}},
		{ByteRange: turn.ByteRange{Start: uint(offset + len(first) + 1), End: uint(offset + len(first) + 1 + len(second) + 20)}, Placeholder: &placeholder},
	}
	got := SlashCommandArgsElements(rest, offset, elements)
	want := []turn.TextElement{
		{ByteRange: turn.ByteRange{Start: 0, End: uint(len(first))}},
		{ByteRange: turn.ByteRange{Start: uint(len(first) + 1), End: uint(len(rest))}, Placeholder: &placeholder},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SlashCommandArgsElements = %#v, want %#v", got, want)
	}
	if got := SlashCommandArgsElements("", offset, elements); got != nil {
		t.Fatalf("empty rest should produce nil elements, got %#v", got)
	}
}
