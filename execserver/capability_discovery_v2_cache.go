package execserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type cachedV2Discovery struct {
	response *DiscoverV2CapabilitiesResponse
}

type v2DiscoveryManager struct {
	mu sync.Mutex

	watcher               *fsnotify.Watcher
	registrationSucceeded bool
	dirty                 bool
	entries               map[string]cachedV2Discovery
	prewarmDone           chan struct{}
	prewarmed             []v2CapabilityLocation
	prewarmWarnings       []string
}

func newV2DiscoveryManager() *v2DiscoveryManager {
	m := &v2DiscoveryManager{
		entries: map[string]cachedV2Discovery{}, prewarmDone: make(chan struct{}),
	}
	go m.prewarmLocations()
	return m
}

func (m *v2DiscoveryManager) Snapshot(params *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error) {
	if m == nil {
		return discoverCapabilitiesV2(params)
	}
	<-m.prewarmDone
	m.mu.Lock()
	m.ensureWatcherLocked()
	locations := append([]v2CapabilityLocation(nil), m.prewarmed...)
	warnings := append([]string(nil), m.prewarmWarnings...)
	key := v2DiscoveryCacheKey(params)
	if m.registrationSucceeded && !m.dirty {
		if cached, ok := m.entries[key]; ok {
			m.mu.Unlock()
			return cached.response, nil
		}
	}
	m.mu.Unlock()

	response, err := discoverCapabilitiesV2WithLocations(params, locations, warnings)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.entries[key] = cachedV2Discovery{response: response}
	m.dirty = false
	m.mu.Unlock()
	return response, nil
}

func (m *v2DiscoveryManager) prewarmLocations() {
	locations, warnings := v2DefaultCapabilityLocations()
	m.mu.Lock()
	m.prewarmed = locations
	m.prewarmWarnings = warnings
	m.mu.Unlock()
	close(m.prewarmDone)
}

func (m *v2DiscoveryManager) ensureWatcherLocked() {
	if m.watcher != nil {
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	roots := []string{filepath.Join(fsHelperCodexHome(), "skills"), filepath.Join(fsHelperCodexHome(), "plugins", "cache")}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".agents", "skills"))
	}
	succeeded := true
	for _, root := range roots {
		if !watchExistingAncestor(watcher, root) {
			succeeded = false
		}
	}
	m.watcher = watcher
	m.registrationSucceeded = succeeded
	go func() {
		for {
			select {
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				m.mu.Lock()
				m.dirty = true
				m.mu.Unlock()
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
				m.mu.Lock()
				m.registrationSucceeded = false
				m.dirty = true
				m.mu.Unlock()
			}
		}
	}()
}

func watchExistingAncestor(watcher *fsnotify.Watcher, path string) bool {
	for candidate := filepath.Clean(path); ; candidate = filepath.Dir(candidate) {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return watcher.Add(candidate) == nil
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return false
		}
	}
}

func (m *v2DiscoveryManager) markDirty() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.dirty = true
	m.mu.Unlock()
}

func v2DiscoveryCacheKey(params *DiscoverV2CapabilitiesParams) string {
	data, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	return string(data)
}
