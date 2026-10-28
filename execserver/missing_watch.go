package execserver

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// maxMissingWatchPaths bounds the ancestors and targets one observation may
// cover (Rust #57598 MAX_WATCH_PATHS).
const maxMissingWatchPaths = 64

// missingWatchState keeps only the newest observation on this physical
// connection: a newer watchId supersedes the prior watch.
type missingWatchState struct {
	mu   sync.Mutex
	id   uint64
	stop chan struct{}
}

type missingWatchRegistration struct {
	changed <-chan struct{}
	close   func()
}

// watchMissing registers an unsandboxed observation that every missing path is
// absent (and present, when supplied, exists). Unsupported platforms and
// observations that cannot be verified report watching=false.
func (s *Server) watchMissing(params *FSWatchMissingParams, notify processNotifier) *FSWatchMissingResponse {
	notWatching := &FSWatchMissingResponse{Watching: false}
	if s == nil || params == nil {
		return notWatching
	}
	state := &s.missingWatch
	state.mu.Lock()
	defer state.mu.Unlock()
	if params.WatchID <= state.id {
		return notWatching
	}
	state.id = params.WatchID
	if state.stop != nil {
		close(state.stop)
		state.stop = nil
	}
	if runtime.GOOS != "linux" || len(params.Missing) == 0 || len(params.Missing) > maxMissingWatchPaths {
		return notWatching
	}
	watch, ok := registerMissingWatch(params)
	if !ok {
		return notWatching
	}
	stop := make(chan struct{})
	state.stop = stop
	watchID := params.WatchID
	go func() {
		select {
		case <-stop:
			watch.close()
			return
		case <-watch.changed:
			watch.close()
		}
		if notify != nil {
			notify(MethodFSMissingChanged, FSMissingChangedNotification{WatchID: watchID})
		}
	}()
	return &FSWatchMissingResponse{Watching: true}
}

// invalidateMissingWatch drops the current observation without notifying the
// client. Connection loss and forced reloads use this path (Rust #57598).
func (s *Server) invalidateMissingWatch() {
	if s == nil {
		return
	}
	state := &s.missingWatch
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.stop != nil {
		close(state.stop)
		state.stop = nil
	}
}

func registerMissingWatch(params *FSWatchMissingParams) (*missingWatchRegistration, bool) {
	missing := make([]string, 0, len(params.Missing))
	for _, raw := range params.Missing {
		path, err := resolvePath(raw)
		if err != nil {
			return nil, false
		}
		missing = append(missing, filepath.Clean(path))
	}
	present := ""
	hasPresent := false
	if params.Present != nil {
		path, err := resolvePath(*params.Present)
		if err != nil {
			return nil, false
		}
		present = filepath.Clean(path)
		hasPresent = true
	}

	targets := append([]string(nil), missing...)
	if hasPresent {
		targets = append(targets, present)
	}
	parents := map[string]bool{}
	for _, target := range targets {
		for parent := filepath.Dir(target); ; parent = filepath.Dir(parent) {
			parents[parent] = true
			if len(parents) > maxMissingWatchPaths {
				return nil, false
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
	}
	orderedParents := make([]string, 0, len(parents))
	for parent := range parents {
		orderedParents = append(orderedParents, parent)
	}
	// Ancestors sort before their descendants, so a missing parent created
	// during setup invalidates through its already-watched parent.
	sort.Strings(orderedParents)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, false
	}
	fail := func() (*missingWatchRegistration, bool) {
		_ = watcher.Close()
		return nil, false
	}
	for _, parent := range orderedParents {
		info, err := os.Lstat(parent)
		switch {
		case err == nil:
			if !info.IsDir() || !missingWatchFileSystemSupported(parent) {
				return fail()
			}
			if err := watcher.Add(parent); err != nil {
				return fail()
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return fail()
		}
	}
	for _, path := range missing {
		if _, err := os.Lstat(path); err == nil {
			return fail()
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail()
		}
	}
	if hasPresent {
		info, err := os.Lstat(present)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				return fail()
			}
		case errors.Is(err, os.ErrNotExist):
			return fail()
		default:
			return fail()
		}
	}

	changed := make(chan struct{}, 1)
	mounts, stopMounts := missingWatchMountSource()
	var once sync.Once
	signal := func() { once.Do(func() { close(changed) }) }
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					signal()
					return
				}
				if missingWatchEventAffectsTargets(event, targets) {
					signal()
					return
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					signal()
					return
				}
				signal()
				return
			case <-mounts:
				signal()
				return
			}
		}
	}()
	return &missingWatchRegistration{
		changed: changed,
		close: func() {
			stopMounts()
			_ = watcher.Close()
		},
	}, true
}

// missingWatchEventAffectsTargets preserves the spelling the executor wrote,
// even in casefolded directories. A non-ASCII component is resolved by the next
// scan using the filesystem's own rules (Rust event_may_affect_target).
func missingWatchEventAffectsTargets(event fsnotify.Event, targets []string) bool {
	if event.Op == fsnotify.Chmod {
		return false
	}
	changed := filepath.Clean(event.Name)
	for _, target := range targets {
		if missingWatchPathMayAffect(changed, target) {
			return true
		}
	}
	return false
}

func missingWatchPathMayAffect(path string, target string) bool {
	pathParts := splitPathComponents(path)
	targetParts := splitPathComponents(target)
	if len(pathParts) > len(targetParts) {
		return false
	}
	for index, name := range pathParts {
		expected := targetParts[index]
		if !strings.EqualFold(name, expected) && isASCII(name) && isASCII(expected) {
			return false
		}
	}
	return true
}

func splitPathComponents(path string) []string {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	var parts []string
	if volume != "" {
		parts = append(parts, volume)
	}
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }) {
		parts = append(parts, part)
	}
	return parts
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}
