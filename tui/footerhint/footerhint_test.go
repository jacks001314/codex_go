package footerhint

import (
	"reflect"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestWrapHintRowsWrapsWholeHintsUsingDisplayWidth(t *testing.T) {
	hints := []string{"\u2191 \u4e0a", "\u2193 \u4e0b", "enter select"}
	got := WrapHintRows(hints, 9, 1, runewidth.StringWidth)
	want := [][]string{{"\u2191 \u4e0a", "\u2193 \u4e0b"}, {"enter select"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
}

func TestWrapHintRowsRetainsOversizedHintsAndAnEmptyRow(t *testing.T) {
	got := WrapHintRows([]string{"ctrl+x stop", "esc back"}, 0, 3, func(hint string) int { return len(hint) })
	want := [][]string{{"ctrl+x stop"}, {"esc back"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
	empty := WrapHintRows([]string{}, 40, 3, func(hint string) int { return len(hint) })
	if len(empty) != 1 || len(empty[0]) != 0 {
		t.Fatalf("empty rows = %#v, want one empty row", empty)
	}
}
