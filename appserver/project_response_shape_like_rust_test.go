package appserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #61771: project responses no longer carry numeric positions.
// Clients rely on the returned order, and project/move keeps placing the moved
// project before `beforeProjectId` (or appending it when omitted).
func TestRuntimeRouterProjectResponsesOmitPositionLikeRust(t *testing.T) {
	router, _ := newProjectTestRuntimeRouter(t)
	create := func(id int64, name, key string) Project {
		response := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(id), MethodProjectCreate, ProjectCreateParams{
			Name: name, Roots: []ProjectRoot{{Path: t.TempDir()}}, IdempotencyKey: key,
		}))).(*ProjectCreateResponse)
		return response.Project
	}
	alpha := create(1, "alpha", "shape-1")
	beta := create(2, "beta", "shape-2")
	gamma := create(3, "gamma", "shape-3")

	encoded, err := json.Marshal(alpha)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "position") {
		t.Fatalf("project response exposes a position: %s", encoded)
	}
	wantKeys := []string{`"id"`, `"name"`, `"roots"`, `"metadata"`, `"createdAt"`, `"updatedAt"`, `"recencyAt"`}
	last := -1
	for _, key := range wantKeys {
		index := strings.Index(string(encoded), key)
		if index <= last {
			t.Fatalf("project key %s missing or out of Rust order: %s", key, encoded)
		}
		last = index
	}

	move := func(id int64, projectID string, before *string) {
		t.Helper()
		projectResponseResult(t, router.Handle(requestWithParams(t, IntID(id), MethodProjectMove, ProjectMoveParams{
			ProjectID: projectID, BeforeProjectID: before,
		})))
	}
	move(4, gamma.ID, &alpha.ID)
	if order := projectListIDs(t, router, 5); strings.Join(order, ",") != strings.Join([]string{gamma.ID, alpha.ID, beta.ID}, ",") {
		t.Fatalf("move before alpha order = %v", order)
	}
	move(6, gamma.ID, nil)
	if order := projectListIDs(t, router, 7); strings.Join(order, ",") != strings.Join([]string{alpha.ID, beta.ID, gamma.ID}, ",") {
		t.Fatalf("move to end order = %v", order)
	}
}

func projectListIDs(t *testing.T, router *RuntimeRouter, id int64) []string {
	t.Helper()
	list := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(id), MethodProjectList, ProjectListParams{}))).(*ProjectListResponse)
	ids := make([]string, 0, len(list.Data))
	for _, project := range list.Data {
		ids = append(ids, project.ID)
	}
	return ids
}
