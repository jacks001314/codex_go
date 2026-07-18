package bottompane

import "reflect"
import "testing"

func TestTabBarLinesWrapAndMarkActiveLikeRust(t *testing.T) {
	tabs := []SelectionTab{
		{ID: "all", Label: "All"},
		{ID: "enabled", Label: "Enabled"},
		{ID: "disabled", Label: "Disabled"},
	}
	got := TabBarLines(tabs, 1, 18)
	want := []string{"All  [Enabled]", "Disabled"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
	if height := TabBarHeight(tabs, 1, 18); height != 2 {
		t.Fatalf("height = %d, want 2", height)
	}
}

func TestTabBarEmptyAndNarrow(t *testing.T) {
	if got := TabBarHeight(nil, 0, 10); got != 0 {
		t.Fatalf("empty height = %d", got)
	}
	got := TabBarLines([]SelectionTab{{Label: "LongTab"}, {Label: "B"}}, 0, 1)
	want := []string{"[LongTab]", "B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrow lines = %#v, want %#v", got, want)
	}
}

func TestTabBarLinesUseDisplayWidth(t *testing.T) {
	got := TabBarLines([]SelectionTab{{Label: "模型"}, {Label: "B"}}, 0, 7)
	want := []string{"[模型]", "B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wide tab lines = %#v, want %#v", got, want)
	}
}
