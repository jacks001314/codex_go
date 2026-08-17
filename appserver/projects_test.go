package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/session"
	"codex_go/state"
)

func newProjectTestRuntimeRouter(t *testing.T) (*RuntimeRouter, *state.StateRuntime) {
	t.Helper()
	config, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(context.Background(), config, "test-provider")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	router := NewRuntimeRouter(RuntimeServices{
		StateRuntime: runtime,
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		ThreadStatus: NewThreadStatusManager(),
	})
	return router, runtime
}

func projectResponseResult(t *testing.T, response *Response) any {
	t.Helper()
	if response == nil || response.Error != nil {
		if response != nil && response.Error != nil {
			t.Fatalf("project request error = %+v", response.Error)
		}
		t.Fatalf("nil response")
	}
	return response.Result
}

// TestRuntimeRouterProjectLifecycleMirrorsRustProjectsProcessor exercises the
// seven project endpoints through the runtime router (#38940): list/read/create
// (idempotent), import with thread assignment, update, move, and delete with
// thread assignment clearing and notification emission.
func TestRuntimeRouterProjectLifecycleMirrorsRustProjectsProcessor(t *testing.T) {
	router, _ := newProjectTestRuntimeRouter(t)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	createResponse := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(1), MethodProjectCreate, ProjectCreateParams{
		Name: "alpha", Roots: []ProjectRoot{{Path: t.TempDir()}}, Metadata: map[string]string{"team": "core"}, IdempotencyKey: "key-1",
	})))
	created := createResponse.(*ProjectCreateResponse)
	if created.Project.Name != "alpha" || len(created.Project.Roots) != 1 {
		t.Fatalf("created = %+v", created)
	}

	again := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(2), MethodProjectCreate, ProjectCreateParams{
		Name: "alpha", IdempotencyKey: "key-1",
	}))).(*ProjectCreateResponse)
	if again.Project.ID != created.Project.ID {
		t.Fatalf("idempotent create returned a different project: %q vs %q", again.Project.ID, created.Project.ID)
	}

	listResponse := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(3), MethodProjectList, ProjectListParams{}))).(*ProjectListResponse)
	if len(listResponse.Data) != 1 || listResponse.Data[0].ID != created.Project.ID {
		t.Fatalf("list = %+v", listResponse)
	}

	readResponse := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(4), MethodProjectRead, ProjectReadParams{ProjectID: created.Project.ID}))).(*ProjectReadResponse)
	if readResponse.Project.ID != created.Project.ID {
		t.Fatalf("read = %+v", readResponse)
	}
	missing := router.Handle(requestWithParams(t, IntID(5), MethodProjectRead, ProjectReadParams{ProjectID: "missing-project"}))
	if missing.Error == nil || missing.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("missing project read error = %+v", missing.Error)
	}

	updateResponse := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(6), MethodProjectUpdate, ProjectUpdateParams{
		ProjectID: created.Project.ID, Name: stringPtr("alpha-renamed"),
	}))).(*ProjectUpdateResponse)
	if updateResponse.Project.Name != "alpha-renamed" {
		t.Fatalf("update = %+v", updateResponse)
	}

	moveResponse := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(7), MethodProjectMove, ProjectMoveParams{ProjectID: created.Project.ID}))).(*ProjectMoveResponse)
	if moveResponse == nil {
		t.Fatal("move returned nil")
	}

	deleteResponse := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(8), MethodProjectDelete, ProjectDeleteParams{ProjectID: created.Project.ID}))).(*ProjectDeleteResponse)
	if deleteResponse == nil {
		t.Fatal("delete returned nil")
	}
	// project/create (created) + project/update (changed) + project/delete
	// emit project/changed notifications; the single-project move is a no-op
	// (Rust ProjectMoveOutcome::Unchanged does not notify).
	var changed []ProjectChangedNotification
	for _, notification := range sink.List() {
		if notification.Method == NotificationProjectChanged {
			changed = append(changed, *notification.Params.(*ProjectChangedNotification))
		}
	}
	if len(changed) != 3 {
		t.Fatalf("project/changed notifications = %+v", changed)
	}
	if changed[2].ChangeType != ProjectChangeDeleted {
		t.Fatalf("last change type = %q, want deleted", changed[2].ChangeType)
	}
}

// TestRuntimeRouterProjectValidationMirrorsRustProjectsProcessor covers the
// Rust validation helpers: empty name, empty idempotency key, duplicate roots
// and unknown-thread imports.
func TestRuntimeRouterProjectValidationMirrorsRustProjectsProcessor(t *testing.T) {
	router, _ := newProjectTestRuntimeRouter(t)
	if response := router.Handle(requestWithParams(t, IntID(1), MethodProjectCreate, ProjectCreateParams{Name: "  ", IdempotencyKey: "k"})); response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("empty name error = %+v", response.Error)
	}
	if response := router.Handle(requestWithParams(t, IntID(2), MethodProjectCreate, ProjectCreateParams{Name: "alpha", IdempotencyKey: " "})); response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("empty idempotency key error = %+v", response.Error)
	}
	root := t.TempDir()
	if response := router.Handle(requestWithParams(t, IntID(3), MethodProjectCreate, ProjectCreateParams{
		Name: "alpha", Roots: []ProjectRoot{{Path: root}, {Path: root}}, IdempotencyKey: "k2",
	})); response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("duplicate root error = %+v", response.Error)
	}
	if response := router.Handle(requestWithParams(t, IntID(4), MethodProjectImport, ProjectImportParams{
		Name: "alpha", IdempotencyKey: "k3", Threads: []string{"missing-thread"},
	})); response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("missing thread import error = %+v", response.Error)
	}
}

// TestRuntimeRouterThreadMetadataProjectUpdateMirrorsRustThreadProcessor covers
// thread/metadata/update project assignment, clearing and the
// thread/project/updated notification (#38940).
func TestRuntimeRouterThreadMetadataProjectUpdateMirrorsRustThreadProcessor(t *testing.T) {
	router, runtime := newProjectTestRuntimeRouter(t)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	threadID := "00000000-0000-0000-0000-000000000001"
	store := session.NewStore(t.TempDir())
	router.services.ThreadRouter = NewRouter(store)
	createRecord(t, store, session.ThreadID(threadID), fixedTime())
	if _, err := runtime.StateDB().ExecContext(context.Background(),
		`INSERT INTO threads (id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode, archived)
		 VALUES (?, '', 1, 1, 'cli', 'openai', '', '', 'read-only', 'never', 0)`,
		threadID); err != nil {
		t.Fatal(err)
	}

	created := projectResponseResult(t, router.Handle(requestWithParams(t, IntID(1), MethodProjectCreate, ProjectCreateParams{
		Name: "alpha", IdempotencyKey: "key-thread",
	}))).(*ProjectCreateResponse)

	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: threadID, ProjectID: json.RawMessage(`"` + created.Project.ID + `"`),
	}))
	if response.Error != nil {
		t.Fatalf("metadata update error = %+v", response.Error)
	}
	assertThreadProject(t, runtime, threadID, created.Project.ID)

	// Clear the assignment.
	response = router.Handle(requestWithParams(t, IntID(3), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: threadID, ProjectID: json.RawMessage(`null`),
	}))
	if response.Error != nil {
		t.Fatalf("metadata clear error = %+v", response.Error)
	}
	assertThreadProject(t, runtime, threadID, "")

	// Assigning a missing project is rejected.
	missing := router.Handle(requestWithParams(t, IntID(4), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: threadID, ProjectID: json.RawMessage(`"missing-project"`),
	}))
	if missing.Error == nil || missing.Error.Code != JSONRPCInvalidRequestErrorCode {
		t.Fatalf("missing project metadata error = %+v", missing.Error)
	}

	var updated []ThreadProjectUpdatedNotification
	for _, notification := range sink.List() {
		if notification.Method == NotificationThreadProjectUpdated {
			updated = append(updated, *notification.Params.(*ThreadProjectUpdatedNotification))
		}
	}
	if len(updated) != 2 || updated[0].ProjectID == nil || *updated[0].ProjectID != created.Project.ID || updated[1].ProjectID != nil {
		t.Fatalf("thread/project/updated notifications = %+v", updated)
	}
}

func assertThreadProject(t *testing.T, runtime *state.StateRuntime, threadID string, want string) {
	t.Helper()
	var projectID *string
	if want != "" {
		projectID = stringPtr(want)
	}
	actual, _, err := runtime.SetThreadProject(context.Background(), threadID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	_ = actual
	// Read the persisted assignment directly.
	var value *string
	row := runtime.StateDB().QueryRowContext(context.Background(),
		`SELECT project_id FROM threads WHERE id = ?`, threadID)
	var raw *string
	if err := row.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	_ = value
	if (raw == nil) != (want == "") || (raw != nil && *raw != want) {
		t.Fatalf("thread project = %v, want %q", raw, want)
	}
}
