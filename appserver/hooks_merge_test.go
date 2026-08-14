package appserver

import (
	"testing"
)

func TestMergeHookListResponsesPreservesWarningsAndErrors(t *testing.T) {
	left := &HookListResponse{Data: []HookListEntry{
		{CWD: "/a", Warnings: []string{"w1"}, Errors: []HookErrorInfo{{Path: "/a/hook.json", Message: "e1"}}, RequiredLoadErrors: []string{"r1"}},
	}}
	right := &HookListResponse{Data: []HookListEntry{
		{CWD: "/a", Warnings: []string{"w2"}},
		{CWD: "/b", Errors: []HookErrorInfo{{Path: "/b/hook.json", Message: "e2"}}},
	}}
	merged := MergeHookListResponses(left, right)
	if len(merged.Data) != 2 {
		t.Fatalf("merged entries = %#v", merged.Data)
	}
	byCWD := map[string]HookListEntry{}
	for _, entry := range merged.Data {
		byCWD[entry.CWD] = entry
	}
	if len(byCWD["/a"].Warnings) != 2 || len(byCWD["/a"].Errors) != 1 || len(byCWD["/a"].RequiredLoadErrors) != 1 || len(byCWD["/b"].Errors) != 1 {
		t.Fatalf("merged = %#v", merged.Data)
	}
}

func TestCloneEntryPreservesRequiredLoadErrors(t *testing.T) {
	entry := HookListEntry{CWD: "/a", RequiredLoadErrors: []string{"bad hook"}}
	cloned := cloneEntry(entry)
	if len(cloned.RequiredLoadErrors) != 1 || cloned.RequiredLoadErrors[0] != "bad hook" {
		t.Fatalf("cloned = %#v", cloned)
	}
	cloned.RequiredLoadErrors[0] = "mutated"
	if entry.RequiredLoadErrors[0] != "bad hook" {
		t.Fatal("cloneEntry shared the RequiredLoadErrors backing array")
	}
}
