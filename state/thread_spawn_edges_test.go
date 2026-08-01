package state

import (
	"context"
	"reflect"
	"testing"
)

func TestThreadSpawnEdgesPersistRustOrderingAndStatusSemantics(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	for _, edge := range []struct {
		parent string
		child  string
		status string
	}{
		{"root", "b", "open"},
		{"root", "a", "open"},
		{"root", "future", "future"},
		{"a", "z", "open"},
		{"b", "c", "open"},
		{"c", "closed", "closed"},
	} {
		if err := runtime.UpsertThreadSpawnEdge(ctx, edge.parent, edge.child, edge.status); err != nil {
			t.Fatal(err)
		}
	}

	children, err := runtime.ListThreadSpawnChildren(ctx, "root", nil)
	if err != nil || !reflect.DeepEqual(children, []string{"a", "b", "future"}) {
		t.Fatalf("all children = %#v, %v", children, err)
	}
	open := "open"
	children, err = runtime.ListThreadSpawnChildren(ctx, "root", &open)
	if err != nil || !reflect.DeepEqual(children, []string{"a", "b"}) {
		t.Fatalf("open children = %#v, %v", children, err)
	}
	descendants, err := runtime.ListThreadSpawnDescendants(ctx, "root", nil)
	if err != nil || !reflect.DeepEqual(descendants, []string{"a", "b", "future", "c", "z", "closed"}) {
		t.Fatalf("all descendants = %#v, %v", descendants, err)
	}
	descendants, err = runtime.ListThreadSpawnDescendants(ctx, "root", &open)
	if err != nil || !reflect.DeepEqual(descendants, []string{"a", "b", "c", "z"}) {
		t.Fatalf("open descendants = %#v, %v", descendants, err)
	}
}

func TestThreadSpawnEdgesUpsertStatusAndCycleProtection(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	if err := runtime.UpsertThreadSpawnEdge(ctx, "old-parent", "child", "open"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.UpsertThreadSpawnEdge(ctx, "new-parent", "child", "closed"); err != nil {
		t.Fatal(err)
	}
	children, err := runtime.ListThreadSpawnChildren(ctx, "old-parent", nil)
	if err != nil || len(children) != 0 {
		t.Fatalf("old parent children = %#v, %v", children, err)
	}
	closed := "closed"
	children, err = runtime.ListThreadSpawnChildren(ctx, "new-parent", &closed)
	if err != nil || !reflect.DeepEqual(children, []string{"child"}) {
		t.Fatalf("new parent children = %#v, %v", children, err)
	}
	if err := runtime.SetThreadSpawnEdgeStatus(ctx, "child", "open"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetThreadSpawnEdgeStatus(ctx, "missing", "closed"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.UpsertThreadSpawnEdge(ctx, "child", "new-parent", "open"); err != nil {
		t.Fatal(err)
	}
	descendants, err := runtime.ListThreadSpawnDescendants(ctx, "new-parent", nil)
	if err != nil || !reflect.DeepEqual(descendants, []string{"child"}) {
		t.Fatalf("cyclic descendants = %#v, %v", descendants, err)
	}
}
