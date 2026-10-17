package historycell

import (
	"reflect"
	"testing"
)

func TestComputerActivityCellActivityIDsDeduplicateCallIDsLikeRust(t *testing.T) {
	cell := ComputerActivityCell{Calls: []McpToolCallCell{
		{CallID: " call-a "},
		{CallID: "call-b"},
		{CallID: "call-a"},
		{CallID: ""},
	}}
	if got, want := cell.ActivityIDs(), []string{"call-a", "call-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ActivityIDs() = %#v, want %#v", got, want)
	}
}
