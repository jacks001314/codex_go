package codemode

import (
	"context"
	"reflect"
	"testing"
)

// TestCodeModeArraySortSurvivesComparatorMutationLikeRust mirrors the observable
// half of Rust #45760: a sort comparator that mutates its receiver must not
// corrupt the result or throw, and ordinary numeric sorting still returns
// [1,2,3]. Rust additionally disables the V8 Maglev/Turbolev/TurboFan array
// builtin inlining because the pinned V8 can store an object in a Smi-elements
// array; Go executes code-mode cells with the pure-Go Sobek engine, which has no
// element-kind JIT, so this test guards the shared behaviour instead.
func TestCodeModeArraySortSurvivesComparatorMutationLikeRust(t *testing.T) {
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: `
function sortWithMutation(values) {
  return values.sort(() => {
    values.fill(0);
    return 0;
  });
}
for (let i = 0; i < 100; ++i) {
  sortWithMutation([1, 2]);
  sortWithMutation([{}, {}]);
}
const object = {};
const values = [object, {}];
sortWithMutation(values);
if (values[0] === object) {
  throw new Error("sort kept a mutated element in place");
}
text(JSON.stringify([3, 1, 2].sort((a, b) => a - b)));
`})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []ContentItem{InputText("[1,2,3]")}
	if !reflect.DeepEqual(result.ContentItems, want) {
		t.Fatalf("content items = %#v, want %#v", result.ContentItems, want)
	}
}
