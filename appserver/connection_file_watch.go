package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var ErrInvalidConnectionFileWatch = errors.New("invalid watch")

type ConnectionFileWatchParams struct {
	ConnectionID string `json:"connectionId"`
	WatchID      string `json:"watchId"`
	Path         string `json:"path"`
	Recursive    bool   `json:"recursive,omitempty"`
}

type ConnectionFileWatchResponse struct {
	Path string `json:"path"`
}

type ConnectionFileUnwatchParams struct {
	ConnectionID string `json:"connectionId"`
	WatchID      string `json:"watchId"`
}

type ConnectionFileUnwatchResponse struct{}

type ConnectionFileChangedNotification struct {
	ConnectionID string   `json:"connectionId"`
	WatchID      string   `json:"watchId"`
	ChangedPaths []string `json:"changedPaths"`
}

func (n *ConnectionFileChangedNotification) MarshalJSON() ([]byte, error) {
	changedPaths := append([]string(nil), n.ChangedPaths...)
	if changedPaths == nil {
		changedPaths = []string{}
	}
	return json.Marshal(struct {
		ConnectionID string   `json:"connectionId"`
		WatchID      string   `json:"watchId"`
		ChangedPaths []string `json:"changedPaths"`
	}{
		ConnectionID: n.ConnectionID,
		WatchID:      n.WatchID,
		ChangedPaths: changedPaths,
	})
}

type connectionFileWatchKey struct {
	connectionID string
	watchID      string
}

type connectionFileWatchEntry struct {
	path      string
	recursive bool
}

type ConnectionFileWatchManager struct {
	mu      sync.Mutex
	watches map[connectionFileWatchKey]connectionFileWatchEntry
}

func NewConnectionFileWatchManager() *ConnectionFileWatchManager {
	return &ConnectionFileWatchManager{watches: map[connectionFileWatchKey]connectionFileWatchEntry{}}
}

func (m *ConnectionFileWatchManager) Watch(params *ConnectionFileWatchParams) (*ConnectionFileWatchResponse, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidConnectionFileWatch)
	}
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidConnectionFileWatch)
	}
	if strings.TrimSpace(params.ConnectionID) == "" || strings.TrimSpace(params.WatchID) == "" {
		return nil, fmt.Errorf("%w: connectionId and watchId are required", ErrInvalidConnectionFileWatch)
	}
	if strings.TrimSpace(params.Path) == "" {
		return nil, fmt.Errorf("%w: path is required", ErrInvalidConnectionFileWatch)
	}
	path, err := filepath.Abs(params.Path)
	if err != nil {
		return nil, err
	}
	k := connectionFileWatchKey{connectionID: params.ConnectionID, watchID: params.WatchID}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.watches[k]; exists {
		return nil, fmt.Errorf("%w: watchId already exists: %s", ErrInvalidConnectionFileWatch, params.WatchID)
	}
	m.watches[k] = connectionFileWatchEntry{path: path, recursive: params.Recursive}
	return &ConnectionFileWatchResponse{Path: path}, nil
}

func (m *ConnectionFileWatchManager) Unwatch(params *ConnectionFileUnwatchParams) (*ConnectionFileUnwatchResponse, error) {
	if m == nil {
		return &ConnectionFileUnwatchResponse{}, nil
	}
	if params == nil {
		return &ConnectionFileUnwatchResponse{}, nil
	}
	k := connectionFileWatchKey{connectionID: params.ConnectionID, watchID: params.WatchID}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.watches, k)
	return &ConnectionFileUnwatchResponse{}, nil
}

func (m *ConnectionFileWatchManager) ConnectionClosed(connectionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.watches {
		if k.connectionID == connectionID {
			delete(m.watches, k)
		}
	}
}

func (m *ConnectionFileWatchManager) Changed(path string) []ConnectionFileChangedNotification {
	if m == nil {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	notifications := []ConnectionFileChangedNotification{}
	for k, entry := range m.watches {
		if !matchesConnectionFileWatch(entry, abs) {
			continue
		}
		notifications = append(notifications, ConnectionFileChangedNotification{
			ConnectionID: k.connectionID,
			WatchID:      k.watchID,
			ChangedPaths: []string{abs},
		})
	}
	sort.SliceStable(notifications, func(i int, j int) bool {
		if notifications[i].ConnectionID != notifications[j].ConnectionID {
			return notifications[i].ConnectionID < notifications[j].ConnectionID
		}
		return notifications[i].WatchID < notifications[j].WatchID
	})
	return notifications
}

func (m *ConnectionFileWatchManager) WatchCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.watches)
}

func matchesConnectionFileWatch(watch connectionFileWatchEntry, path string) bool {
	if watch.recursive {
		rel, err := filepath.Rel(watch.path, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return filepath.Clean(watch.path) == filepath.Clean(path)
}
