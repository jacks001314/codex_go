package bottompane

import (
	"reflect"
	"testing"
)

// TestPickerAreasMatchesRustAllocation mirrors selection_picker_layout.rs:
// optional gaps and indicators yield before the header, tabs, search, and
// minimum result row.
func TestPickerAreasMatchesRustAllocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		area   PickerRect
		sizes  PickerLayoutSizes
		height []int
	}{
		{
			name: "full mention frame",
			area: PickerRect{X: 3, Y: 5, Width: 80, Height: 16},
			sizes: PickerLayoutSizes{
				Header: 2, HeaderGap: 1, Tabs: 1, Search: 1, Rows: 8,
			},
			height: []int{2, 1, 1, 1, 1, 1, 8, 1, 0, 0},
		},
		{
			name: "short mention frame",
			area: PickerRect{Width: 40, Height: 6},
			sizes: PickerLayoutSizes{
				Header: 2, HeaderGap: 1, Tabs: 1, Search: 1, Rows: 8,
			},
			height: []int{2, 0, 1, 0, 1, 0, 2, 0, 0, 0},
		},
		{
			name: "one row retains tabs",
			area: PickerRect{Width: 40, Height: 1},
			sizes: PickerLayoutSizes{
				Header: 2, HeaderGap: 1, Tabs: 1, Search: 1, Rows: 8,
			},
			height: []int{0, 0, 1, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name: "header yields before controls",
			area: PickerRect{Width: 40, Height: 4},
			sizes: PickerLayoutSizes{
				Header: 2, HeaderGap: 1, Tabs: 1, Search: 1, Rows: 8,
			},
			height: []int{1, 0, 1, 0, 1, 0, 1, 0, 0, 0},
		},
		{
			name: "side content reserves gap",
			area: PickerRect{Width: 40, Height: 13},
			sizes: PickerLayoutSizes{
				Header: 1, HeaderGap: 1, Tabs: 1, Search: 1, Rows: 3, Side: 2,
			},
			height: []int{1, 1, 1, 1, 1, 1, 3, 1, 1, 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			areas := PickerAreas(tc.area, tc.sizes)
			got := make([]int, len(areas))
			y := tc.area.Y
			for index, area := range areas {
				got[index] = area.Height
				if area.X != tc.area.X || area.Y != y || area.Width != tc.area.Width {
					t.Fatalf("area %d = %#v, want x=%d y=%d width=%d", index, area, tc.area.X, y, tc.area.Width)
				}
				y += area.Height
			}
			if !reflect.DeepEqual(got, tc.height) {
				t.Fatalf("heights = %#v, want %#v", got, tc.height)
			}
		})
	}
}

func TestPickerAreasNeverOverallocate(t *testing.T) {
	sizes := PickerLayoutSizes{Header: 2, HeaderGap: 1, Tabs: 1, Search: 1, Rows: 8, Side: 2}
	for height := 0; height <= 40; height++ {
		areas := PickerAreas(PickerRect{Width: 80, Height: height}, sizes)
		total := 0
		for _, area := range areas {
			if area.Height < 0 {
				t.Fatalf("height %d produced negative area %#v", height, area)
			}
			total += area.Height
		}
		if total > height {
			t.Fatalf("height %d allocated %d rows", height, total)
		}
	}
}
