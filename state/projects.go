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
	"strconv"
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
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Roots       []ProjectRoot     `json:"roots"`
	Metadata    map[string]string `json:"metadata"`
	Position    int64             `json:"position"`
	CreatedAt   int64             `json:"createdAt"`
	UpdatedAt   int64             `json:"updatedAt"`
	CreatedAtMS int64             `json:"-"`
	UpdatedAtMS int64             `json:"-"`
	// RecencyAtMS is the newest non-archived assigned thread's recency (Unix ms),
	// used for recency sorting (#41223). Null when no non-archived thread is assigned.
	RecencyAtMS sql.NullInt64 `json:"-"`
}

// ProjectSortKey mirrors Rust state::ProjectSortKey (#41223).
type ProjectSortKey string

const (
	ProjectSortPosition  ProjectSortKey = "position"
	ProjectSortRecencyAt ProjectSortKey = "recencyAt"
)

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

// ListProjects returns a paginated project listing ordered by manual position or
// recency (Rust list_projects after #41223). limit is clamped by callers; the
// store fetches limit+1 rows to compute the next cursor. Recency sorting places
// projects with no assigned non-archived thread last.
func (r *StateRuntime) ListProjects(ctx context.Context, cursor *string, limit int, sortKey ProjectSortKey, direction string) (*ProjectsPage, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit < 1 {
		limit = 50
	}
	if sortKey == "" {
		sortKey = ProjectSortPosition
	}
	if direction != "asc" && direction != "desc" {
		direction = "asc"
		if sortKey == ProjectSortRecencyAt {
			direction = "desc"
		}
	}
	anchor, err := parseProjectCursor(cursor, sortKey, direction)
	if err != nil {
		return nil, err
	}
	column := "p.position"
	operator := ">"
	orderColumn := "p.position"
	nullsLast := false
	if sortKey == ProjectSortRecencyAt {
		column = "p.recency_at_ms"
		orderColumn = "p.recency_at_ms IS NULL ASC, p.recency_at_ms"
		nullsLast = true
	}
	if direction == "desc" {
		operator = "<"
	}
	directionSQL := "ASC"
	if direction == "desc" {
		directionSQL = "DESC"
	}
	var where string
	var args []any
	if anchor != nil {
		if anchor.value != nil {
			where = fmt.Sprintf("WHERE (%[1]s %[2]s ? OR (%[1]s = ? AND p.id %[2]s ?))", column, operator)
			if nullsLast {
				where += " OR p.recency_at_ms IS NULL"
			}
			args = append(args, *anchor.value, *anchor.value, anchor.id)
		} else {
			where = "WHERE p.recency_at_ms IS NULL AND p.id " + operator + " ?"
			args = append(args, anchor.id)
		}
	}
	query := fmt.Sprintf(
		`WITH project_activity AS (
			SELECT id, name, metadata, position, created_at_ms, updated_at_ms,
			       (SELECT MAX(recency_at_ms) FROM threads WHERE project_id = projects.id AND archived = 0) AS recency_at_ms
			FROM projects
		), page AS (
			SELECT * FROM project_activity p %s ORDER BY %s %s, p.id %s LIMIT ?
		)
		SELECT p.*, roots.path AS root_path FROM page p
		LEFT JOIN project_roots roots ON roots.project_id = p.id
		ORDER BY %s %s, p.id %s, roots.position ASC`,
		where, orderColumn, directionSQL, directionSQL, orderColumn, directionSQL, directionSQL)
	args = append(args, limit+1)
	rows, err := r.stateDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var rootPath *string
		project, err := scanProjectRowWithOptionalRoot(rows, &rootPath)
		if err != nil {
			return nil, err
		}
		if len(projects) > 0 && projects[len(projects)-1].ID == project.ID {
			if rootPath != nil {
				projects[len(projects)-1].Roots = append(projects[len(projects)-1].Roots, ProjectRoot{Path: *rootPath})
			}
			continue
		}
		if rootPath != nil {
			project.Roots = append(project.Roots, ProjectRoot{Path: *rootPath})
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var nextCursor *string
	if len(projects) > limit {
		projects = projects[:limit]
		value := projectCursor(projects[limit-1], sortKey, direction)
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
		`SELECT id, name, metadata, position, created_at_ms, updated_at_ms,
		        (SELECT MAX(recency_at_ms) FROM threads WHERE project_id = projects.id AND archived = 0) AS recency_at_ms
		 FROM projects WHERE id = ?`, id)
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
	var recencyAtMS sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT (SELECT MAX(recency_at_ms) FROM threads WHERE project_id = ? AND archived = 0)`,
		id).Scan(&recencyAtMS); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &CreatedProject{
		Project: Project{
			ID: id, Name: name, Roots: cloneProjectRoots(roots), Metadata: cloneStringMap(metadata),
			Position: position, CreatedAtMS: now, UpdatedAtMS: now, RecencyAtMS: recencyAtMS,
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
		RecencyAtMS: project.RecencyAtMS,
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
	var recencyAtMS sql.NullInt64
	if err := scanner.Scan(&id, &name, &metadataJSON, &position, &createdAtMS, &updatedAtMS, &recencyAtMS); err != nil {
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
		RecencyAtMS: recencyAtMS,
	}, nil
}

// scanProjectRowWithOptionalRoot scans the project columns plus a nullable root
// path, used by the joined project/list query.
func scanProjectRowWithOptionalRoot(scanner rowScanner, rootPath **string) (Project, error) {
	var id, name, metadataJSON string
	var position, createdAtMS, updatedAtMS int64
	var recencyAtMS sql.NullInt64
	if err := scanner.Scan(&id, &name, &metadataJSON, &position, &createdAtMS, &updatedAtMS, &recencyAtMS, rootPath); err != nil {
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
		RecencyAtMS: recencyAtMS,
	}, nil
}

func (r *StateRuntime) getProjectInTx(ctx context.Context, tx *sql.Tx, id string) (*Project, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, name, metadata, position, created_at_ms, updated_at_ms,
		        (SELECT MAX(recency_at_ms) FROM threads WHERE project_id = projects.id AND archived = 0) AS recency_at_ms
		 FROM projects WHERE id = ?`, id)
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
	value *int64
	id    string
}

func projectCursor(project Project, sortKey ProjectSortKey, direction string) string {
	if sortKey == ProjectSortPosition && direction == "asc" {
		// Retain the legacy ascending-position format for older clients.
		return fmt.Sprintf("%d|%s", project.Position, project.ID)
	}
	key := string(sortKey)
	value := "null"
	if sortKey == ProjectSortPosition {
		value = fmt.Sprintf("%d", project.Position)
	} else {
		if project.RecencyAtMS.Valid {
			value = fmt.Sprintf("%d", project.RecencyAtMS.Int64)
		}
	}
	return fmt.Sprintf("v1|%s|%s|%s|%s", key, direction, value, project.ID)
}

func parseProjectCursor(cursor *string, sortKey ProjectSortKey, direction string) (*projectCursorValue, error) {
	if cursor == nil || strings.TrimSpace(*cursor) == "" {
		return nil, nil
	}
	raw := strings.TrimSpace(*cursor)
	if len(raw) > 128 {
		return nil, fmt.Errorf("invalid project cursor: malformed or mismatched sort anchor")
	}
	parts := strings.Split(raw, "|")
	key := string(sortKey)
	var valueStr, id string
	switch {
	case len(parts) == 2 && sortKey == ProjectSortPosition && direction == "asc":
		valueStr, id = parts[0], parts[1]
	case len(parts) == 5 && parts[0] == "v1" && parts[1] == key && parts[2] == direction:
		valueStr, id = parts[3], parts[4]
	default:
		return nil, fmt.Errorf("invalid project cursor: malformed or mismatched sort anchor")
	}
	parsedID, err := uuid.Parse(id)
	if err != nil || parsedID.String() != id {
		return nil, fmt.Errorf("invalid project cursor: malformed or mismatched sort anchor")
	}
	var value *int64
	if valueStr == "null" && sortKey == ProjectSortRecencyAt {
		value = nil
	} else {
		parsed, err := strconv.ParseInt(valueStr, 10, 64)
		if err != nil || strconv.FormatInt(parsed, 10) != valueStr || (sortKey == ProjectSortPosition && parsed < 0) {
			return nil, fmt.Errorf("invalid project cursor: malformed or mismatched sort anchor")
		}
		value = &parsed
	}
	return &projectCursorValue{value: value, id: id}, nil
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
