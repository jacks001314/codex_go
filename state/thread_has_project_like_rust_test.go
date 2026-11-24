package state

import (
	"context"
	"testing"
)

// Mirrors Rust #63545: the staged project decides only until the thread has a
// row in SQLite, and an existing row's assignment always wins.
func TestThreadHasProjectLikeRust(t *testing.T) {
	ctx := context.Background()
	runtime := newProjectTestRuntime(t)
	seedProjectThread(t, runtime, "assigned-thread", false)
	seedProjectThread(t, runtime, "plain-thread", false)
	project, err := runtime.CreateProject(ctx, "alpha", []ProjectRoot{{Path: "/repo"}}, nil, nil, "key-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := runtime.SetThreadProject(ctx, "assigned-thread", &project.Project.ID); err != nil || !found {
		t.Fatalf("SetThreadProject() = %v, %v", found, err)
	}

	if assigned, err := runtime.ThreadHasProject(ctx, "assigned-thread", nil); err != nil || !assigned {
		t.Fatalf("assigned thread = %v, %v, want true", assigned, err)
	}
	if assigned, err := runtime.ThreadHasProject(ctx, "plain-thread", nil); err != nil || assigned {
		t.Fatalf("plain thread = %v, %v, want false", assigned, err)
	}
	staged := project.Project.ID
	if assigned, err := runtime.ThreadHasProject(ctx, "unknown-thread", &staged); err != nil || !assigned {
		t.Fatalf("staged project = %v, %v, want true", assigned, err)
	}
	if assigned, err := runtime.ThreadHasProject(ctx, "unknown-thread", nil); err != nil || assigned {
		t.Fatalf("no staged project = %v, %v, want false", assigned, err)
	}
	missing := "missing-project"
	if assigned, err := runtime.ThreadHasProject(ctx, "unknown-thread", &missing); err != nil || assigned {
		t.Fatalf("missing staged project = %v, %v, want false", assigned, err)
	}
	if assigned, err := runtime.ThreadHasProject(ctx, "plain-thread", &staged); err != nil || assigned {
		t.Fatalf("plain thread with staged project = %v, %v, want false", assigned, err)
	}
}
