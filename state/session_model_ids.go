package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"codex_go/modelfacingid"
)

// SessionModelIDStore issues stable model-facing aliases shared by a session's
// root and workers (Rust #61339). A fork copies the source snapshot into its
// own session before issuing an alias of its own.
type SessionModelIDStore interface {
	Lookup(ctx context.Context, sessionID string, key modelfacingid.ModelIDKey) (*modelfacingid.ID, error)
	GetOrAssign(ctx context.Context, sessionID string, key modelfacingid.ModelIDKey) (modelfacingid.ID, error)
	Snapshot(ctx context.Context, sessionID string) ([]modelfacingid.ModelIDMap, error)
	Initialize(ctx context.Context, sessionID string, maps []modelfacingid.ModelIDMap) error
}

// Lookup returns the alias bound to key, if the session has already assigned it.
func (r *StateRuntime) Lookup(ctx context.Context, sessionID string, key modelfacingid.ModelIDKey) (*modelfacingid.ID, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	return lookupSessionModelID(ctx, r.stateDB, sessionID, key)
}

// GetOrAssign commits the assignment before returning it so the model can never
// observe an alias that is not durable. The key is rechecked under the write
// transaction because another writer may have assigned it since the first read.
func (r *StateRuntime) GetOrAssign(ctx context.Context, sessionID string, key modelfacingid.ModelIDKey) (modelfacingid.ID, error) {
	if r == nil || r.stateDB == nil {
		return modelfacingid.ID{}, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, err := lookupSessionModelID(ctx, r.stateDB, sessionID, key); err != nil || existing != nil {
		if err != nil {
			return modelfacingid.ID{}, err
		}
		return *existing, nil
	}
	tx, err := BeginImmediateTx(ctx, r.stateDB)
	if err != nil {
		return modelfacingid.ID{}, fmt.Errorf("begin session model id assignment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if existing, err := lookupSessionModelID(ctx, tx, sessionID, key); err != nil {
		return modelfacingid.ID{}, err
	} else if existing != nil {
		if err := tx.Commit(); err != nil {
			return modelfacingid.ID{}, fmt.Errorf("commit session model id lookup: %w", err)
		}
		return *existing, nil
	}
	var storedInitial sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT initial_state FROM session_model_ids WHERE session_id = ? AND kind = ? AND ordinal = 0`,
		sessionID, key.Kind,
	).Scan(&storedInitial)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return modelfacingid.ID{}, fmt.Errorf("read session model id generator: %w", err)
	}
	initial := storedInitial.String
	if !storedInitial.Valid {
		seed, err := randomModelIDSeed()
		if err != nil {
			return modelfacingid.ID{}, err
		}
		generator, err := modelfacingid.New(seed, key.Kind)
		if err != nil {
			return modelfacingid.ID{}, err
		}
		encoded, err := json.Marshal(generator.Snapshot())
		if err != nil {
			return modelfacingid.ID{}, err
		}
		initial = string(encoded)
	}
	var ordinal int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO session_model_ids (session_id, kind, external_key, ordinal, initial_state)
SELECT ?, ?, ?, COALESCE(MAX(ordinal) + 1, 0), ? FROM session_model_ids
WHERE session_id = ? AND kind = ?
RETURNING ordinal`,
		sessionID, key.Kind, key.Key, nullableInitial(!storedInitial.Valid, initial), sessionID, key.Kind,
	).Scan(&ordinal)
	if err != nil {
		return modelfacingid.ID{}, fmt.Errorf("assign session model id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return modelfacingid.ID{}, fmt.Errorf("commit session model id assignment: %w", err)
	}
	state, err := decodeModelIDState(initial)
	if err != nil {
		return modelfacingid.ID{}, err
	}
	return state.IDAt(uint64(ordinal))
}

// Snapshot returns every namespace's generator state and ordered keys.
func (r *StateRuntime) Snapshot(ctx context.Context, sessionID string) ([]modelfacingid.ModelIDMap, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := r.stateDB.QueryContext(ctx, `
SELECT kind, ordinal, initial_state, external_key FROM session_model_ids
WHERE session_id = ? ORDER BY kind, ordinal`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read session model id snapshot: %w", err)
	}
	defer rows.Close()
	maps := make([]modelfacingid.ModelIDMap, 0, 1)
	for rows.Next() {
		var kind, externalKey string
		var ordinal int64
		var initial sql.NullString
		if err := rows.Scan(&kind, &ordinal, &initial, &externalKey); err != nil {
			return nil, err
		}
		if len(maps) == 0 || maps[len(maps)-1].Initial.Salt != kind {
			if !initial.Valid {
				return nil, fmt.Errorf("session model id namespace %q has no initial state", kind)
			}
			state, err := decodeModelIDState(initial.String)
			if err != nil {
				return nil, err
			}
			maps = append(maps, modelfacingid.ModelIDMap{Initial: state})
		}
		maps[len(maps)-1].Keys = append(maps[len(maps)-1].Keys, externalKey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return maps, nil
}

// Initialize installs a fork's copied alias maps before it can issue new ones.
func (r *StateRuntime) Initialize(ctx context.Context, sessionID string, maps []modelfacingid.ModelIDMap) error {
	if r == nil || r.stateDB == nil {
		return errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := BeginImmediateTx(ctx, r.stateDB)
	if err != nil {
		return fmt.Errorf("begin session model id initialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, idMap := range maps {
		encodedInitial, err := json.Marshal(idMap.Initial)
		if err != nil {
			return err
		}
		encodedKeys, err := json.Marshal(idMap.Keys)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO session_model_ids (session_id, kind, external_key, ordinal, initial_state)
SELECT ?, ?, value, key, CASE WHEN key = 0 THEN ? END FROM json_each(?)`,
			sessionID, idMap.Initial.Salt, string(encodedInitial), string(encodedKeys),
		); err != nil {
			return fmt.Errorf("initialize session model id namespace %q: %w", idMap.Initial.Salt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session model id initialization: %w", err)
	}
	return nil
}

type sessionModelIDQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func lookupSessionModelID(ctx context.Context, querier sessionModelIDQuerier, sessionID string, key modelfacingid.ModelIDKey) (*modelfacingid.ID, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var initial sql.NullString
	var ordinal int64
	err := querier.QueryRowContext(ctx, `
SELECT first.initial_state, bound.ordinal FROM session_model_ids bound
JOIN session_model_ids first
  ON first.session_id = bound.session_id AND first.kind = bound.kind AND first.ordinal = 0
WHERE bound.session_id = ? AND bound.kind = ? AND bound.external_key = ?`,
		sessionID, key.Kind, key.Key,
	).Scan(&initial, &ordinal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup session model id: %w", err)
	}
	if !initial.Valid {
		return nil, fmt.Errorf("session model id namespace %q has no initial state", key.Kind)
	}
	state, err := decodeModelIDState(initial.String)
	if err != nil {
		return nil, err
	}
	id, err := state.IDAt(uint64(ordinal))
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func decodeModelIDState(raw string) (modelfacingid.State, error) {
	var state modelfacingid.State
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return modelfacingid.State{}, fmt.Errorf("decode session model id generator: %w", err)
	}
	return state, nil
}

func nullableInitial(first bool, initial string) any {
	if first {
		return initial
	}
	return nil
}

func randomModelIDSeed() (uint64, error) {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return 0, fmt.Errorf("generate session model id seed: %w", err)
	}
	return binary.LittleEndian.Uint64(bytes[:]), nil
}

func deleteSessionModelIDs(ctx context.Context, tx *ImmediateTx, sessionID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_model_ids WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete model ids for session %s: %w", sessionID, err)
	}
	return nil
}

// MemorySessionModelIDStore provides the same alias semantics for ephemeral
// sessions and in-memory thread stores (Rust #61339).
type MemorySessionModelIDStore struct {
	mu       sync.Mutex
	sessions map[string][]modelfacingid.ModelIDMap
}

// NewMemorySessionModelIDStore returns an empty in-memory alias store.
func NewMemorySessionModelIDStore() *MemorySessionModelIDStore {
	return &MemorySessionModelIDStore{sessions: map[string][]modelfacingid.ModelIDMap{}}
}

// Remove drops every alias belonging to a deleted session.
func (s *MemorySessionModelIDStore) Remove(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// Lookup returns the alias bound to key, if any.
func (s *MemorySessionModelIDStore) Lookup(_ context.Context, sessionID string, key modelfacingid.ModelIDKey) (*modelfacingid.ID, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, idMap := range s.sessions[sessionID] {
		if idMap.Initial.Salt != key.Kind {
			continue
		}
		return idMap.Lookup(key.Key)
	}
	return nil, nil
}

// GetOrAssign reuses an existing alias or extends the namespace by one key.
func (s *MemorySessionModelIDStore) GetOrAssign(_ context.Context, sessionID string, key modelfacingid.ModelIDKey) (modelfacingid.ID, error) {
	if s == nil {
		return modelfacingid.ID{}, errors.New("memory session model id store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string][]modelfacingid.ModelIDMap{}
	}
	maps := s.sessions[sessionID]
	index := -1
	for candidate := range maps {
		if maps[candidate].Initial.Salt == key.Kind {
			index = candidate
			break
		}
	}
	if index < 0 {
		seed, err := randomModelIDSeed()
		if err != nil {
			return modelfacingid.ID{}, err
		}
		idMap, err := modelfacingid.NewModelIDMap(seed, key.Kind)
		if err != nil {
			return modelfacingid.ID{}, err
		}
		maps = append(maps, idMap)
		index = len(maps) - 1
	}
	if id, err := maps[index].Lookup(key.Key); err != nil || id != nil {
		if err != nil {
			return modelfacingid.ID{}, err
		}
		return *id, nil
	}
	id, err := maps[index].Initial.IDAt(uint64(len(maps[index].Keys)))
	if err != nil {
		return modelfacingid.ID{}, err
	}
	maps[index].Keys = append(maps[index].Keys, key.Key)
	s.sessions[sessionID] = maps
	return id, nil
}

// Snapshot returns a copy of every namespace's state and ordered keys.
func (s *MemorySessionModelIDStore) Snapshot(_ context.Context, sessionID string) ([]modelfacingid.ModelIDMap, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	maps := s.sessions[sessionID]
	clone := make([]modelfacingid.ModelIDMap, 0, len(maps))
	for _, idMap := range maps {
		clone = append(clone, modelfacingid.ModelIDMap{
			Initial: idMap.Initial,
			Keys:    append([]string(nil), idMap.Keys...),
		})
	}
	return clone, nil
}

// Initialize replaces a session's alias maps.
func (s *MemorySessionModelIDStore) Initialize(_ context.Context, sessionID string, maps []modelfacingid.ModelIDMap) error {
	if s == nil {
		return errors.New("memory session model id store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string][]modelfacingid.ModelIDMap{}
	}
	clone := make([]modelfacingid.ModelIDMap, 0, len(maps))
	for _, idMap := range maps {
		clone = append(clone, modelfacingid.ModelIDMap{
			Initial: idMap.Initial,
			Keys:    append([]string(nil), idMap.Keys...),
		})
	}
	s.sessions[sessionID] = clone
	return nil
}
