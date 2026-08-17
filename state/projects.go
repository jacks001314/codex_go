package state

// Rust parity: codex-rs/state/src/runtime/projects.rs (#38940). SQLite-backed
// project store with ordered roots, metadata, manual positioning, cursor
// pagination and idempotent creation, plus thread project assignment.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProjectRoot is one ordered root path of a project.
type ProjectRoot struct {
	Path string `json:"path"`
}

// Project is the stored project record.
type Project struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Roots      []ProjectRoot     `json:"roots"`
	Metadata   map[string]string `json:"metadata"`
	Position   int64             `json:"position"`
	CreatedAt  int64             `json:"createdAt"`
	UpdatedAt  int64             `json:"updatedAt"`
	CreatedAtMS int64            `json:"-"`
	UpdatedAtMS int64            `json:"-"`
}

// CreatedProject reports the created project and whether it was newly created
// (false when the idempotency key already resolved to a project).
type CreatedProject struct {
	Project Project
	Created bool
}

// ProjectsPage is a paginated project listing.
type ProjectsPage struct {
	Projects   []Project
	NextCursor *string
}

// ProjectChangeType mirrors Rust project/changed change types.
type ProjectChangeType string

const (
	ProjectChangeCreated ProjectChangeType = "created"
	ProjectChangeUpdated ProjectChangeType = "updated"
	ProjectChangeDeleted ProjectChangeType = "deleted"
)

// ProjectChangedNotification mirrors Rust ProjectChangedNotification.
type ProjectChangedNotification struct {
	ProjectID  string            `json:"projectId"`
	ChangeType ProjectChangeType `json:"changeType"`
}

// ThreadProjectUpdatedNotification mirrors Rust ThreadProjectUpdatedNotification.
type ThreadProjectUpdatedNotification struct {
	ThreadID  string  `json:"threadId"`
	ProjectID *string `json:"projectId"`
}

// SetThreadProject assigns (or clears) a thread's project assignment. It
// returns (previous, true) when the thread exists, (nil, false) when the
// thread is unknown, and an error when the project does not exist (Rust
// set_thread_project).
func (r *StateRuntime) SetThreadProject(ctx context.Context, threadID string, projectID *string) (previous *string, found bool, err error) {
	if r == nil || r.stateDB == nil {
		return nil, false, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.stateDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if projectID != nil && strings.TrimSpace(*projectID) != "" {
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, *projectID).Scan(&count); err != nil {
			return nil, false, err
		}
		if count == 0 {
			return nil, false, fmt.Errorf("project not found: %s", *projectID)
		}
	}
	var current sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT project_id FROM threads WHERE id = ?`, threadID).Scan(&current)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var previousValue *string
	if current.Valid {
		previousValue = &current.String
	}
	next := (*string)(nil)
	if projectID != nil && strings.TrimSpace(*projectID) != "" {
		value := strings.TrimSpace(*projectID)
		next = &value
	}
	if (previousValue == nil) != (next == nil) || (previousValue != nil && next != nil && *previousValue != *next) {
		if _, err := tx.ExecContext(ctx, `UPDATE threads SET project_id = ? WHERE id = ?`, nullableStringPointer(next), threadID); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return previousValue, true, nil
}

// ListProjects returns a paginated project listing ordered by position then id
// (Rust list_projects). limit is clamped to 1..100 by callers; the store
// fetches limit+1 rows to compute the next cursor.
func (r *StateRuntime) ListProjects(ctx context.Context, cursor *string, limit int) (*ProjectsPage, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit < 1 {
		limit = 50
	}
	anchor, err := parseProjectCursor(cursor)
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	if anchor != nil {
		rows, err = r.stateDB.QueryContext(ctx,
			`SELECT id, name, metadata, position, created_at_ms, updated_at_ms FROM projects
			 WHERE position > ? OR (position = ? AND id > ?)
			 ORDER BY position ASC, id ASC LIMIT ?`,
			anchor.position, anchor.position, anchor.id, limit+1)
	} else {
		rows, err = r.stateDB.QueryContext(ctx,
			`SELECT id, name, metadata, position, created_at_ms, updated_at_ms FROM projects
			 ORDER BY position ASC, id ASC LIMIT ?`, limit+1)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		project, err := scanProjectRow(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	rootsByID, err := r.projectRootsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range projects {
		projects[i].Roots = rootsByID[projects[i].ID]
	}
	var nextCursor *string
	if len(projects) > limit {
		projects = projects[:limit]
		value := projectCursor(projects[limit-1])
		nextCursor = &value
	}
	return &ProjectsPage{Projects: projects, NextCursor: nextCursor}, nil
}

// GetProject reads one project by id.
func (r *StateRuntime) GetProject(ctx context.Context, id string) (*Project, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	row := r.stateDB.QueryRowContext(ctx,
		`SELECT id, name, metadata, position, created_at_ms, updated_at_ms FROM projects WHERE id = ?`, id)
	project, err := scanProjectRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	roots, err := r.projectRootsByID(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	project.Roots = roots[id]
	return &project, nil
}

// GetProjectByIdempotencyKey resolves a project by its creation idempotency
// key (Rust get_project_by_idempotency_key).
func (r *StateRuntime) GetProjectByIdempotencyKey(ctx context.Context, idempotencyKey string) (*Project, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var projectID string
	err := r.stateDB.QueryRowContext(ctx,
		`SELECT project_id FROM project_idempotency_keys WHERE key = ?`, idempotencyKey).Scan(&projectID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	project, err := r.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("idempotency key refers to deleted project: %s", idempotencyKey)
	}
	return project, nil
}

// CreateProject creates a project (or resolves the existing project for the
// idempotency key), assigns the optional threads, and records the idempotency
// key (Rust create_project).
func (r *StateRuntime) CreateProject(ctx context.Context, name string, roots []ProjectRoot, metadata map[string]string, threadIDs []string, idempotencyKey string) (*CreatedProject, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.stateDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingProjectID string
	err = tx.QueryRowContext(ctx,
		`SELECT project_id FROM project_idempotency_keys WHERE key = ?`, idempotencyKey).Scan(&existingProjectID)
	if err == nil {
		project, err := r.getProjectInTx(ctx, tx, existingProjectID)
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, fmt.Errorf("idempotency key refers to deleted project: %s", idempotencyKey)
		}
		return &CreatedProject{Project: *project, Created: false}, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	for _, threadID := range threadIDs {
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM threads WHERE id = ?`, threadID).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, fmt.Errorf("thread not found: %s", threadID)
		}
	}
	id := uuid.NewString()
	now := nowMilliseconds()
	var maxPosition sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(position) FROM projects`).Scan(&maxPosition); err != nil {
		return nil, err
	}
	position := int64(-1)
	if maxPosition.Valid {
		position = maxPosition.Int64
	}
	position++
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projects (id, name, metadata, position, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		id, name, string(metadataJSON), position, now, now); err != nil {
		return nil, err
	}
	if err := replaceProjectRoots(ctx, tx, id, roots); err != nil {
		return nil, err
	}
	for _, threadID := range threadIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE threads SET project_id = ? WHERE id = ?`, id, threadID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_idempotency_keys (key, project_id, created_at_ms) VALUES (?, ?, ?)`,
		idempotencyKey, id, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &CreatedProject{
		Project: Project{
			ID: id, Name: name, Roots: cloneProjectRoots(roots), Metadata: cloneStringMap(metadata),
			Position: position, CreatedAtMS: now, UpdatedAtMS: now,
		},
		Created: true,
	}, nil
}

// UpdateProject updates a project's name, roots and/or metadata. It returns
// (nil, nil) when the project is unknown and (project, changed) otherwise
// (Rust update_project).
func (r *StateRuntime) UpdateProject(ctx context.Context, id string, name *string, roots *[]ProjectRoot, metadata *map[string]string) (*Project, *bool, error) {
	if r == nil || r.stateDB == nil {
		return nil, nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.stateDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	project, err := r.getProjectInTx(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	if project == nil {
		return nil, nil, nil
	}
	nextName := project.Name
	if name != nil {
		nextName = *name
	}
	nextRoots := cloneProjectRoots(project.Roots)
	if roots != nil {
		nextRoots = cloneProjectRoots(*roots)
	}
	nextMetadata := cloneStringMap(project.Metadata)
	if metadata != nil {
		nextMetadata = cloneStringMap(*metadata)
	}
	if nextName == project.Name && projectRootsEqual(nextRoots, project.Roots) && stringMapsEqual(nextMetadata, project.Metadata) {
		return project, boolPtr(false), tx.Commit()
	}
	now := nowMilliseconds()
	metadataJSON, err := json.Marshal(nextMetadata)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET name = ?, metadata = ?, updated_at_ms = ? WHERE id = ?`,
		nextName, string(metadataJSON), now, id); err != nil {
		return nil, nil, err
	}
	if !projectRootsEqual(nextRoots, project.Roots) {
		if err := replaceProjectRoots(ctx, tx, id, nextRoots); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return &Project{
		ID: id, Name: nextName, Roots: nextRoots, Metadata: nextMetadata,
		Position: project.Position, CreatedAtMS: project.CreatedAtMS, UpdatedAtMS: now,
	}, boolPtr(true), nil
}

// MoveProject reorders a project before another project (or appends when the
// anchor is absent). It returns (nil, nil) when the project is unknown and
// (changed, nil) otherwise (Rust move_project).
func (r *StateRuntime) MoveProject(ctx context.Context, projectID string, beforeProjectID *string) (*bool, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.stateDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM projects ORDER BY position ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	currentIndex := -1
	for i, id := range ids {
		if id == projectID {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return nil, nil
	}
	if beforeProjectID != nil && *beforeProjectID == projectID {
		return nil, fmt.Errorf("project %s cannot be moved before itself", projectID)
	}
	original := append([]string(nil), ids...)
	ids = append(ids[:currentIndex], ids[currentIndex+1:]...)
	nextIndex := len(ids)
	if beforeProjectID != nil {
		found := -1
		for i, id := range ids {
			if id == *beforeProjectID {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("before project not found: %s", *beforeProjectID)
		}
		nextIndex = found
	}
	ids = append(ids[:nextIndex], append([]string{projectID}, ids[nextIndex:]...)...)
	if strings.Join(ids, "\x00") == strings.Join(original, "\x00") {
		return boolPtr(false), tx.Commit()
	}
	for position, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET position = ? WHERE id = ?`, position, id); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at_ms = ? WHERE id = ?`, nowMilliseconds(), projectID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return boolPtr(true), nil
}

// DeleteProject deletes a project, clearing thread assignments without
// deleting threads. It returns (activeThreadIDs, archivedThreadIDs, true) when
// the project existed and (nil, nil, false, nil) when it was unknown (Rust
// delete_project).
func (r *StateRuntime) DeleteProject(ctx context.Context, id string) (active []string, archived []string, found bool, err error) {
	if r == nil || r.stateDB == nil {
		return nil, nil, false, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.stateDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, id).Scan(&count); err != nil {
		return nil, nil, false, err
	}
	if count == 0 {
		return nil, nil, false, nil
	}
	active, err = projectThreadIDs(ctx, tx, id, false)
	if err != nil {
		return nil, nil, false, err
	}
	archived, err = projectThreadIDs(ctx, tx, id, true)
	if err != nil {
		return nil, nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE threads SET project_id = NULL WHERE project_id = ?`, id); err != nil {
		return nil, nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return nil, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	return active, archived, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProjectRow(scanner rowScanner) (Project, error) {
	var id, name, metadataJSON string
	var position, createdAtMS, updatedAtMS int64
	if err := scanner.Scan(&id, &name, &metadataJSON, &position, &createdAtMS, &updatedAtMS); err != nil {
		return Project{}, err
	}
	metadata := map[string]string{}
	if strings.TrimSpace(metadataJSON) != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			return Project{}, fmt.Errorf("project metadata is not a string map: %w", err)
		}
	}
	return Project{
		ID: id, Name: name, Metadata: metadata, Position: position,
		CreatedAtMS: createdAtMS, UpdatedAtMS: updatedAtMS,
		CreatedAt: createdAtMS / 1000, UpdatedAt: updatedAtMS / 1000,
	}, nil
}

func (r *StateRuntime) getProjectInTx(ctx context.Context, tx *sql.Tx, id string) (*Project, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, name, metadata, position, created_at_ms, updated_at_ms FROM projects WHERE id = ?`, id)
	project, err := scanProjectRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT path FROM project_roots WHERE project_id = ? ORDER BY position ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		project.Roots = append(project.Roots, ProjectRoot{Path: path})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &project, nil
}

func (r *StateRuntime) projectRootsByID(ctx context.Context, projectIDs []string) (map[string][]ProjectRoot, error) {
	out := map[string][]ProjectRoot{}
	if len(projectIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(projectIDs)), ",")
	args := make([]any, 0, len(projectIDs))
	for _, id := range projectIDs {
		args = append(args, id)
	}
	rows, err := r.stateDB.QueryContext(ctx,
		`SELECT project_id, path FROM project_roots WHERE project_id IN (`+placeholders+`) ORDER BY project_id ASC, position ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID, path string
		if err := rows.Scan(&projectID, &path); err != nil {
			return nil, err
		}
		out[projectID] = append(out[projectID], ProjectRoot{Path: path})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func replaceProjectRoots(ctx context.Context, tx *sql.Tx, projectID string, roots []ProjectRoot) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_roots WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	for position, root := range roots {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)`,
			projectID, position, root.Path); err != nil {
			return err
		}
	}
	return nil
}

func projectThreadIDs(ctx context.Context, tx *sql.Tx, projectID string, archived bool) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM threads WHERE project_id = ? AND archived = ? ORDER BY id ASC`, projectID, archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type projectCursorValue struct {
	position int64
	id       string
}

func projectCursor(project Project) string {
	return fmt.Sprintf("%d|%s", project.Position, project.ID)
}

func parseProjectCursor(cursor *string) (*projectCursorValue, error) {
	if cursor == nil || strings.TrimSpace(*cursor) == "" {
		return nil, nil
	}
	raw := strings.TrimSpace(*cursor)
	parts := strings.Split(raw, "|")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid project cursor: %s", raw)
	}
	var position int64
	if _, err := fmt.Sscanf(parts[0], "%d", &position); err != nil {
		return nil, fmt.Errorf("invalid project cursor: %s", raw)
	}
	if fmt.Sprintf("%d", position) != parts[0] || position < 0 || strings.TrimSpace(parts[1]) == "" {
		return nil, fmt.Errorf("invalid project cursor: %s", raw)
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return nil, fmt.Errorf("invalid project cursor: %s", raw)
	}
	return &projectCursorValue{position: position, id: parts[1]}, nil
}

func cloneProjectRoots(roots []ProjectRoot) []ProjectRoot {
	if roots == nil {
		return nil
	}
	out := make([]ProjectRoot, len(roots))
	copy(out, roots)
	return out
}

func projectRootsEqual(a []ProjectRoot, b []ProjectRoot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringMapsEqual(a map[string]string, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func boolPtr(value bool) *bool {
	return &value
}

// nowMilliseconds is a small indirection so tests can freeze time if needed.
var nowMilliseconds = func() int64 {
	return time.Now().UTC().UnixMilli()
}

func sortedProjectIDs(projects []Project) []string {
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	sort.Strings(ids)
	return ids
}
