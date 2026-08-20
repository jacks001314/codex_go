package tui

import (
	"reflect"
	"testing"

	"codex_go/appserver"
)

func TestFileUpdateChangesToDisplayIncludesMoveDestinations(t *testing.T) {
	movePath := "src/new.txt"
	changes := []appserver.FileUpdateChange{
		{Path: "src/old.txt", Kind: appserver.PatchChangeKind{Type: "update", MovePath: &movePath}, Diff: "@@"},
		{Path: "src/other.txt", Kind: appserver.PatchChangeKind{Type: "add"}, Diff: "hello\n"},
		{Path: "src/old.txt", Kind: appserver.PatchChangeKind{Type: "update", MovePath: &movePath}, Diff: "@@"},
	}
	got := FileUpdateChangesToDisplay(changes)
	want := []string{"src/old.txt -> src/new.txt", "src/other.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FileUpdateChangesToDisplay = %#v, want %#v", got, want)
	}
}

func TestFileUpdateChangesToDisplaySkipsEmptyAndDedupes(t *testing.T) {
	got := FileUpdateChangesToDisplay([]appserver.FileUpdateChange{
		{Path: "  ", Kind: appserver.PatchChangeKind{Type: "add"}},
		{Path: "a.txt", Kind: appserver.PatchChangeKind{Type: "delete"}},
		{Path: "a.txt", Kind: appserver.PatchChangeKind{Type: "delete"}},
	})
	if !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Fatalf("FileUpdateChangesToDisplay = %#v, want [a.txt]", got)
	}
}
