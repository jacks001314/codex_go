package agent

import (
	"reflect"
	"testing"
)

func TestMemoryStoreChildrenAndDescendants(t *testing.T) {
	store := NewMemoryStore()
	if err := store.UpsertThreadSpawnEdge("root", "child-b", ThreadSpawnEdgeOpen); err != nil {
		t.Fatalf("UpsertThreadSpawnEdge() error = %v", err)
	}
	_ = store.UpsertThreadSpawnEdge("root", "child-a", ThreadSpawnEdgeClosed)
	_ = store.UpsertThreadSpawnEdge("child-b", "grandchild", ThreadSpawnEdgeOpen)
	open := ThreadSpawnEdgeOpen
	children, err := store.ListThreadSpawnChildren("root", nil)
	if err != nil {
		t.Fatalf("ListThreadSpawnChildren() error = %v", err)
	}
	if !reflect.DeepEqual(children, []string{"child-a", "child-b"}) {
		t.Fatalf("children = %#v", children)
	}
	openDescendants, err := store.ListThreadSpawnDescendants("root", &open)
	if err != nil {
		t.Fatalf("ListThreadSpawnDescendants() error = %v", err)
	}
	if !reflect.DeepEqual(openDescendants, []string{"child-b", "grandchild"}) {
		t.Fatalf("open descendants = %#v", openDescendants)
	}
}

func TestMemoryStoreListsDescendantsBreadthFirstWithStatusFilters(t *testing.T) {
	store := NewMemoryStore()
	for _, edge := range []ThreadSpawnEdge{
		{ParentThreadID: "root", ChildThreadID: "child-b", Status: ThreadSpawnEdgeOpen},
		{ParentThreadID: "root", ChildThreadID: "child-a", Status: ThreadSpawnEdgeOpen},
		{ParentThreadID: "child-a", ChildThreadID: "grandchild-open", Status: ThreadSpawnEdgeOpen},
		{ParentThreadID: "child-b", ChildThreadID: "grandchild-closed", Status: ThreadSpawnEdgeClosed},
		{ParentThreadID: "root", ChildThreadID: "child-c-closed", Status: ThreadSpawnEdgeClosed},
		{ParentThreadID: "child-c-closed", ChildThreadID: "great-grandchild-closed", Status: ThreadSpawnEdgeClosed},
	} {
		if err := store.UpsertThreadSpawnEdge(edge.ParentThreadID, edge.ChildThreadID, edge.Status); err != nil {
			t.Fatalf("UpsertThreadSpawnEdge() error = %v", err)
		}
	}

	allDescendants, err := store.ListThreadSpawnDescendants("root", nil)
	if err != nil {
		t.Fatalf("ListThreadSpawnDescendants(all) error = %v", err)
	}
	if !reflect.DeepEqual(allDescendants, []string{
		"child-a",
		"child-b",
		"child-c-closed",
		"grandchild-open",
		"grandchild-closed",
		"great-grandchild-closed",
	}) {
		t.Fatalf("all descendants = %#v", allDescendants)
	}

	open := ThreadSpawnEdgeOpen
	openDescendants, err := store.ListThreadSpawnDescendants("root", &open)
	if err != nil {
		t.Fatalf("ListThreadSpawnDescendants(open) error = %v", err)
	}
	if !reflect.DeepEqual(openDescendants, []string{
		"child-a",
		"child-b",
		"grandchild-open",
	}) {
		t.Fatalf("open descendants = %#v", openDescendants)
	}

	closed := ThreadSpawnEdgeClosed
	closedDescendants, err := store.ListThreadSpawnDescendants("root", &closed)
	if err != nil {
		t.Fatalf("ListThreadSpawnDescendants(closed) error = %v", err)
	}
	if !reflect.DeepEqual(closedDescendants, []string{
		"child-c-closed",
		"great-grandchild-closed",
	}) {
		t.Fatalf("closed descendants = %#v", closedDescendants)
	}
}

func TestMemoryStoreSetMissingIsNoop(t *testing.T) {
	store := NewMemoryStore()
	if err := store.SetThreadSpawnEdgeStatus("missing", ThreadSpawnEdgeClosed); err != nil {
		t.Fatalf("SetThreadSpawnEdgeStatus() error = %v", err)
	}
}
