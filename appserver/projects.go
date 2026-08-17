package appserver

// Rust parity: codex-rs/app-server/src/request_processors/projects.rs and
// app-server-protocol/src/protocol/v2/project.rs (#38940). Experimental
// SQLite-backed project/list, project/read, project/create, project/import,
// project/update, project/move and project/delete endpoints with ordered
// roots, metadata, manual positioning, pagination and idempotent creation,
// plus project/changed and thread/project/updated notifications.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"codex_go/state"
)

const (
	MethodProjectList   Method = "project/list"
	MethodProjectRead   Method = "project/read"
	MethodProjectCreate Method = "project/create"
	MethodProjectImport Method = "project/import"
	MethodProjectUpdate Method = "project/update"
	MethodProjectMove   Method = "project/move"
	MethodProjectDelete Method = "project/delete"

	NotificationProjectChanged      NotificationMethod = "project/changed"
	NotificationThreadProjectUpdated NotificationMethod = "thread/project/updated"

	projectListDefaultLimit = 50
	projectListMaxLimit     = 100
	maxIdempotencyKeyBytes  = 512
)

// ProjectRoot mirrors Rust v2::ProjectRoot: one ordered absolute root path.
type ProjectRoot struct {
	Path string `json:"path"`
}

// Project mirrors Rust v2::Project. Timestamps are seconds since epoch.
type Project struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Roots     []ProjectRoot     `json:"roots"`
	Metadata  map[string]string `json:"metadata"`
	Position  int64             `json:"position"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
}

// ProjectChangeType mirrors Rust v2::ProjectChangeType.
type ProjectChangeType string

const (
	ProjectChangeCreated ProjectChangeType = "created"
	ProjectChangeUpdated ProjectChangeType = "updated"
	ProjectChangeDeleted ProjectChangeType = "deleted"
)

// ProjectChangedNotification mirrors Rust v2::ProjectChangedNotification.
type ProjectChangedNotification struct {
	ProjectID  string            `json:"projectId"`
	ChangeType ProjectChangeType `json:"changeType"`
}

// ThreadProjectUpdatedNotification mirrors Rust
// v2::ThreadProjectUpdatedNotification.
type ThreadProjectUpdatedNotification struct {
	ThreadID  string  `json:"threadId"`
	ProjectID *string `json:"projectId"`
}

type ProjectListParams struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  *uint32 `json:"limit,omitempty"`
}

type ProjectListResponse struct {
	Data       []Project `json:"data"`
	NextCursor *string   `json:"nextCursor,omitempty"`
}

type ProjectReadParams struct {
	ProjectID string `json:"projectId"`
}

type ProjectReadResponse struct {
	Project Project `json:"project"`
}

type ProjectCreateParams struct {
	Name           string            `json:"name"`
	Roots          []ProjectRoot     `json:"roots"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey"`
}

type ProjectCreateResponse struct {
	Project Project `json:"project"`
}

type ProjectImportParams struct {
	Name           string            `json:"name"`
	Roots          []ProjectRoot     `json:"roots"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Threads        []string          `json:"threads,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey"`
}

type ProjectImportResponse struct {
	Project Project `json:"project"`
}

type ProjectUpdateParams struct {
	ProjectID string            `json:"projectId"`
	Name      *string           `json:"name,omitempty"`
	Roots     *[]ProjectRoot    `json:"roots,omitempty"`
	Metadata  *map[string]string `json:"metadata,omitempty"`
}

type ProjectUpdateResponse struct {
	Project Project `json:"project"`
}

type ProjectMoveParams struct {
	ProjectID       string  `json:"projectId"`
	BeforeProjectID *string `json:"beforeProjectId,omitempty"`
}

type ProjectMoveResponse struct{}

type ProjectDeleteParams struct {
	ProjectID string `json:"projectId"`
}

type ProjectDeleteResponse struct{}

// invalidParamsError surfaces as JSON-RPC -32602 (invalid params), mirroring
// Rust's invalid_params helper.
type invalidParamsError struct {
	message string
}

func (e *invalidParamsError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *invalidParamsError) Unwrap() error {
	return ErrInvalidParams
}

func (e *invalidParamsError) Is(target error) bool {
	return target == ErrInvalidParams
}

func invalidParams(message string) error {
	return &invalidParamsError{message: message}
}

func isProjectMethod(method Method) bool {
	switch method {
	case MethodProjectList, MethodProjectRead, MethodProjectCreate, MethodProjectImport,
		MethodProjectUpdate, MethodProjectMove, MethodProjectDelete:
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) dispatchProject(request *Request) (any, error) {
	if r == nil || request == nil {
		return nil, fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	switch request.Method {
	case MethodProjectList:
		return r.handleProjectListRuntime(request)
	case MethodProjectRead:
		return r.handleProjectReadRuntime(request)
	case MethodProjectCreate:
		return r.handleProjectCreateRuntime(request)
	case MethodProjectImport:
		return r.handleProjectImportRuntime(request)
	case MethodProjectUpdate:
		return r.handleProjectUpdateRuntime(request)
	case MethodProjectMove:
		return r.handleProjectMoveRuntime(request)
	case MethodProjectDelete:
		return r.handleProjectDeleteRuntime(request)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}

func (r *RuntimeRouter) handleProjectListRuntime(request *Request) (*ProjectListResponse, error) {
	if r == nil || r.services.StateRuntime == nil {
		return nil, jsonRPCInvalidRequest("project/list is unavailable without sqlite state")
	}
	var params ProjectListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	limit := projectListDefaultLimit
	if params.Limit != nil {
		limit = int(*params.Limit)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > projectListMaxLimit {
		limit = projectListMaxLimit
	}
	page, err := r.services.StateRuntime.ListProjects(context.Background(), params.Cursor, limit)
	if err != nil {
		if isInvalidProjectCursorError(err) {
			return nil, invalidParams(err.Error())
		}
		return nil, fmt.Errorf("failed to run project/list: %w", err)
	}
	response := &ProjectListResponse{NextCursor: page.NextCursor}
	for _, project := range page.Projects {
		response.Data = append(response.Data, apiProjectFromStored(project))
	}
	return response, nil
}

func (r *RuntimeRouter) handleProjectReadRuntime(request *Request) (*ProjectReadResponse, error) {
	if r == nil || r.services.StateRuntime == nil {
		return nil, jsonRPCInvalidRequest("project/read is unavailable without sqlite state")
	}
	var params ProjectReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	project, err := r.services.StateRuntime.GetProject(context.Background(), params.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to run project/read: %w", err)
	}
	if project == nil {
		return nil, invalidParams(fmt.Sprintf("project not found: %s", params.ProjectID))
	}
	return &ProjectReadResponse{Project: apiProjectFromStored(*project)}, nil
}

func (r *RuntimeRouter) handleProjectCreateRuntime(request *Request) (*ProjectCreateResponse, error) {
	if r == nil || r.services.StateRuntime == nil {
		return nil, jsonRPCInvalidRequest("project/create is unavailable without sqlite state")
	}
	var params ProjectCreateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	created, err := r.createProjectRuntime(context.Background(), params.Name, params.Roots, params.Metadata, nil, params.IdempotencyKey, "project/create")
	if err != nil {
		return nil, err
	}
	if created.Created {
		r.notifyProjectChanged(request, created.Project.ID, ProjectChangeCreated)
	}
	return &ProjectCreateResponse{Project: apiProjectFromStored(created.Project)}, nil
}

func (r *RuntimeRouter) handleProjectImportRuntime(request *Request) (*ProjectImportResponse, error) {
	if r == nil || r.services.StateRuntime == nil {
		return nil, jsonRPCInvalidRequest("project/import is unavailable without sqlite state")
	}
	var params ProjectImportParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	threadIDs := params.Threads
	if threadIDs == nil {
		threadIDs = []string{}
	}
	created, err := r.createProjectRuntime(context.Background(), params.Name, params.Roots, params.Metadata, threadIDs, params.IdempotencyKey, "project/import")
	if err != nil {
		return nil, err
	}
	if created.Created {
		r.notifyProjectChanged(request, created.Project.ID, ProjectChangeCreated)
		projectID := created.Project.ID
		for _, threadID := range threadIDs {
			r.notifyThreadProjectUpdated(request, threadID, &projectID)
		}
	}
	return &ProjectImportResponse{Project: apiProjectFromStored(created.Project)}, nil
}

func (r *RuntimeRouter) handleProjectUpdateRuntime(request *Request) (*ProjectUpdateResponse, error) {
	if r == nil || r.services.StateRuntime == nil {
		return nil, jsonRPCInvalidRequest("project/update is unavailable without sqlite state")
	}
	var params ProjectUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	var name *string
	if params.Name != nil {
		value, err := validateProjectName(*params.Name)
		if err != nil {
			return nil, err
		}
		name = &value
	}
	var roots *[]state.ProjectRoot
	if params.Roots != nil {
		validated, err := validateProjectRoots(*params.Roots)
		if err != nil {
			return nil, err
		}
		roots = &validated
	}
	updated, changed, err := r.services.StateRuntime.UpdateProject(context.Background(), params.ProjectID, name, roots, params.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to run project/update: %w", err)
	}
	if updated == nil {
		return nil, invalidParams(fmt.Sprintf("project not found: %s", params.ProjectID))
	}
	project := apiProjectFromStored(*updated)
	if changed != nil && *changed {
		r.notifyProjectChanged(request, project.ID, ProjectChangeUpdated)
	}
	return &ProjectUpdateResponse{Project: project}, nil
}

func (r *RuntimeRouter) handleProjectMoveRuntime(request *Request) (*ProjectMoveResponse, error) {
	if r == nil || r.services.StateRuntime == nil {
		return nil, jsonRPCInvalidRequest("project/move is unavailable without sqlite state")
	}
	var params ProjectMoveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	changed, err := r.services.StateRuntime.MoveProject(context.Background(), params.ProjectID, params.BeforeProjectID)
	if err != nil {
		if isProjectMoveInvalidError(err) {
			return nil, invalidParams(err.Error())
		}
		return nil, fmt.Errorf("failed to run project/move: %w", err)
	}
	if changed == nil {
		return nil, invalidParams(fmt.Sprintf("project not found: %s", params.ProjectID))
	}
	if *changed {
		r.notifyProjectChanged(request, params.ProjectID, ProjectChangeUpdated)
	}
	return &ProjectMoveResponse{}, nil
}

func (r *RuntimeRouter) handleProjectDeleteRuntime(request *Request) (*ProjectDeleteResponse, error) {
	if r == nil || r.services.StateRuntime == nil {
		return nil, jsonRPCInvalidRequest("project/delete is unavailable without sqlite state")
	}
	var params ProjectDeleteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	active, archived, found, err := r.services.StateRuntime.DeleteProject(context.Background(), params.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to run project/delete: %w", err)
	}
	if !found {
		return nil, invalidParams(fmt.Sprintf("project not found: %s", params.ProjectID))
	}
	r.notifyProjectChanged(request, params.ProjectID, ProjectChangeDeleted)
	affected := append(append([]string(nil), active...), archived...)
	for _, threadID := range affected {
		r.notifyThreadProjectUpdated(request, threadID, nil)
	}
	return &ProjectDeleteResponse{}, nil
}

func (r *RuntimeRouter) createProjectRuntime(ctx context.Context, name string, roots []ProjectRoot, metadata map[string]string, threadIDs []string, idempotencyKey string, operation string) (*state.CreatedProject, error) {
	validatedName, err := validateProjectName(name)
	if err != nil {
		return nil, err
	}
	if err := validateProjectIdempotencyKey(idempotencyKey); err != nil {
		return nil, err
	}
	validatedThreads, err := validateProjectThreadIDs(threadIDs)
	if err != nil {
		return nil, err
	}
	validatedRoots, err := validateProjectRoots(roots)
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	created, err := r.services.StateRuntime.CreateProject(ctx, validatedName, validatedRoots, metadata, validatedThreads, idempotencyKey)
	if err != nil {
		if isProjectNotFoundThreadError(err) {
			return nil, invalidParams(err.Error())
		}
		return nil, fmt.Errorf("failed to run %s: %w", operation, err)
	}
	return created, nil
}

func (r *RuntimeRouter) notifyProjectChanged(request *Request, projectID string, changeType ProjectChangeType) {
	if r == nil {
		return
	}
	r.notify(NotificationProjectChanged, &ProjectChangedNotification{
		ProjectID:  projectID,
		ChangeType: changeType,
	})
}

func (r *RuntimeRouter) notifyThreadProjectUpdated(request *Request, threadID string, projectID *string) {
	if r == nil {
		return
	}
	r.notify(NotificationThreadProjectUpdated, &ThreadProjectUpdatedNotification{
		ThreadID:  threadID,
		ProjectID: projectID,
	})
}

func validateProjectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalidParams("project name must not be empty")
	}
	return name, nil
}

func validateProjectIdempotencyKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return invalidParams("idempotencyKey must not be empty")
	}
	if len(key) > maxIdempotencyKeyBytes {
		return invalidParams("idempotencyKey must be at most 512 bytes")
	}
	return nil
}

func validateProjectRoots(roots []ProjectRoot) ([]state.ProjectRoot, error) {
	logical := map[string]struct{}{}
	canonical := map[string]struct{}{}
	out := make([]state.ProjectRoot, 0, len(roots))
	for _, root := range roots {
		path := strings.TrimSpace(root.Path)
		if path == "" || !filepath.IsAbs(path) {
			return nil, invalidParams(fmt.Sprintf("invalid project root: %s", root.Path))
		}
		if _, dup := logical[path]; dup {
			return nil, invalidParams(fmt.Sprintf("duplicate project root: %s", path))
		}
		logical[path] = struct{}{}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			if _, dup := canonical[resolved]; dup {
				return nil, invalidParams(fmt.Sprintf("duplicate resolved project root: %s", path))
			}
			canonical[resolved] = struct{}{}
		}
		out = append(out, state.ProjectRoot{Path: path})
	}
	return out, nil
}

func validateProjectThreadIDs(threadIDs []string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, threadID := range threadIDs {
		threadID = strings.TrimSpace(threadID)
		if _, dup := seen[threadID]; dup {
			return nil, invalidParams(fmt.Sprintf("duplicate thread id: %s", threadID))
		}
		seen[threadID] = struct{}{}
	}
	return threadIDs, nil
}

func apiProjectFromStored(project state.Project) Project {
	return Project{
		ID:        project.ID,
		Name:      project.Name,
		Roots:     storedRootsToAPI(project.Roots),
		Metadata:  cloneStringToStringMap(project.Metadata),
		Position:  project.Position,
		CreatedAt: project.CreatedAt,
		UpdatedAt: project.UpdatedAt,
	}
}

func storedRootsToAPI(roots []state.ProjectRoot) []ProjectRoot {
	out := make([]ProjectRoot, 0, len(roots))
	for _, root := range roots {
		out = append(out, ProjectRoot{Path: root.Path})
	}
	return out
}

func cloneStringToStringMap(source map[string]string) map[string]string {
	if source == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func isInvalidProjectCursorError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "invalid project cursor:")
}

func isProjectMoveInvalidError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.HasPrefix(message, "before project ") ||
		(strings.HasPrefix(message, "project ") && strings.Contains(message, "cannot be moved"))
}

func isProjectNotFoundThreadError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "thread not found") || strings.Contains(message, "project not found")
}
