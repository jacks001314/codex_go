package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	ErrAgentPathExists = errors.New("agent path already exists")
)

const rootAgentPath = "/"

type AgentPath string

type Metadata struct {
	ThreadID        string
	Path            AgentPath
	Nickname        string
	Role            string
	LastTaskMessage string
}

type Registry struct {
	mu                 sync.Mutex
	agentsByPath       map[AgentPath]Metadata
	usedNicknames      map[string]bool
	nicknameResetCount int
	totalCount         atomic.Uint64
	nicknameChooser    *roundRobinChooser
}

type SpawnReservation struct {
	registry         *Registry
	active           bool
	reservedNickname string
	reservedPath     AgentPath
	counted          bool
}

func NewRegistry() *Registry {
	return &Registry{
		agentsByPath:    make(map[AgentPath]Metadata),
		usedNicknames:   make(map[string]bool),
		nicknameChooser: newRoundRobinChooser(),
	}
}

func (r *Registry) ReserveSpawnSlot(maxThreads int) (*SpawnReservation, error) {
	counted := true
	if maxThreads > 0 && !r.tryIncrementSpawned(uint64(maxThreads)) {
		return nil, fmt.Errorf("%w: max_threads=%d", ErrAgentLimitReached, maxThreads)
	}
	if maxThreads <= 0 {
		r.totalCount.Add(1)
	}
	return &SpawnReservation{
		registry: r,
		active:   true,
		counted:  counted,
	}, nil
}

func (r *Registry) RegisterRootThread(threadID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.agentsByPath[rootAgentPath]; exists {
		return
	}
	r.agentsByPath[rootAgentPath] = Metadata{
		ThreadID: threadID,
		Path:     rootAgentPath,
	}
}

func (r *Registry) ReleaseSpawnedThread(threadID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for path, metadata := range r.agentsByPath {
		if metadata.ThreadID != threadID {
			continue
		}
		delete(r.agentsByPath, path)
		if metadata.Nickname != "" {
			delete(r.usedNicknames, metadata.Nickname)
		}
		if path != rootAgentPath {
			r.decrementSpawned()
		}
		return
	}
}

func (r *Registry) RegisterSpawnedThread(metadata Metadata) {
	if metadata.ThreadID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := metadata.Path
	if key == "" {
		key = AgentPath("thread:" + metadata.ThreadID)
	}
	if metadata.Nickname != "" {
		r.usedNicknames[metadata.Nickname] = true
	}
	metadata.Path = key
	r.agentsByPath[key] = metadata
}

func (r *Registry) AgentIDForPath(path AgentPath) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	metadata, ok := r.agentsByPath[path]
	return metadata.ThreadID, ok && metadata.ThreadID != ""
}

func (r *Registry) MetadataForThread(threadID string) (Metadata, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, metadata := range r.agentsByPath {
		if metadata.ThreadID == threadID {
			return metadata, true
		}
	}
	return Metadata{}, false
}

func (r *Registry) LiveAgents() []Metadata {
	r.mu.Lock()
	defer r.mu.Unlock()
	agents := make([]Metadata, 0, len(r.agentsByPath))
	for _, metadata := range r.agentsByPath {
		if metadata.ThreadID == "" || metadata.Path == rootAgentPath {
			continue
		}
		agents = append(agents, metadata)
	}
	sort.SliceStable(agents, func(i int, j int) bool {
		return agents[i].Path < agents[j].Path
	})
	return agents
}

func (r *Registry) UpdateLastTaskMessage(threadID string, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for path, metadata := range r.agentsByPath {
		if metadata.ThreadID == threadID {
			metadata.LastTaskMessage = message
			r.agentsByPath[path] = metadata
			return
		}
	}
}

func (r *Registry) ClearLastTaskMessage(threadID string) {
	r.UpdateLastTaskMessage(threadID, "")
}

func (r *Registry) reserveAgentNickname(names []string, preferred string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	nickname := preferred
	if nickname == "" {
		if len(names) == 0 {
			return "", false
		}
		candidates := make([]string, 0, len(names))
		for _, name := range names {
			formatted := formatAgentNickname(name, r.nicknameResetCount)
			if !r.usedNicknames[formatted] {
				candidates = append(candidates, formatted)
			}
		}
		if len(candidates) == 0 {
			r.usedNicknames = make(map[string]bool)
			r.nicknameResetCount++
			for _, name := range names {
				candidates = append(candidates, formatAgentNickname(name, r.nicknameResetCount))
			}
		}
		nickname = r.nicknameChooser.Choose(candidates)
	}
	r.usedNicknames[nickname] = true
	return nickname, true
}

func (r *Registry) reserveAgentPath(path AgentPath) error {
	if path == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.agentsByPath[path]; exists {
		return fmt.Errorf("%w: %s", ErrAgentPathExists, path)
	}
	r.agentsByPath[path] = Metadata{Path: path}
	return nil
}

func (r *Registry) releaseReservedAgentPath(path AgentPath) {
	if path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if metadata, ok := r.agentsByPath[path]; ok && metadata.ThreadID == "" {
		delete(r.agentsByPath, path)
	}
}

func (r *Registry) tryIncrementSpawned(maxThreads uint64) bool {
	for {
		current := r.totalCount.Load()
		if current >= maxThreads {
			return false
		}
		if r.totalCount.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (r *Registry) TotalSpawned() uint64 {
	return r.totalCount.Load()
}

func (r *Registry) decrementSpawned() {
	for {
		current := r.totalCount.Load()
		if current == 0 {
			return
		}
		if r.totalCount.CompareAndSwap(current, current-1) {
			return
		}
	}
}

func (r *Registry) NicknameResetCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nicknameResetCount
}

func (s *SpawnReservation) ReserveAgentNickname(names []string, preferred string) (string, error) {
	if s == nil || !s.active {
		return "", fmt.Errorf("%w: inactive reservation", ErrAgentLimitReached)
	}
	nickname, ok := s.registry.reserveAgentNickname(names, preferred)
	if !ok {
		return "", errors.New("no agent nicknames available")
	}
	s.reservedNickname = nickname
	return nickname, nil
}

func (s *SpawnReservation) ReserveAgentPath(path AgentPath) error {
	if s == nil || !s.active {
		return fmt.Errorf("%w: inactive reservation", ErrAgentLimitReached)
	}
	if err := s.registry.reserveAgentPath(path); err != nil {
		return err
	}
	s.reservedPath = path
	return nil
}

func (s *SpawnReservation) Commit(metadata Metadata) {
	if s == nil || !s.active {
		return
	}
	if metadata.Nickname == "" {
		metadata.Nickname = s.reservedNickname
	}
	if metadata.Path == "" {
		metadata.Path = s.reservedPath
	}
	s.registry.RegisterSpawnedThread(metadata)
	s.active = false
	s.reservedPath = ""
	s.reservedNickname = ""
}

func (s *SpawnReservation) Cancel() {
	if s == nil || !s.active {
		return
	}
	if s.reservedNickname != "" {
		s.registry.mu.Lock()
		delete(s.registry.usedNicknames, s.reservedNickname)
		s.registry.mu.Unlock()
	}
	if s.reservedPath != "" {
		s.registry.releaseReservedAgentPath(s.reservedPath)
	}
	if s.counted {
		s.registry.decrementSpawned()
	}
	s.active = false
}

func NextThreadSpawnDepth(sessionSource string) int {
	return sessionDepth(sessionSource) + 1
}

func ExceedsThreadSpawnDepthLimit(depth int, maxDepth int) bool {
	return depth > maxDepth
}

func sessionDepth(sessionSource string) int {
	const marker = "depth="
	index := strings.Index(sessionSource, marker)
	if index < 0 {
		return 0
	}
	var depth int
	if _, err := fmt.Sscanf(sessionSource[index+len(marker):], "%d", &depth); err != nil {
		return 0
	}
	return depth
}

func formatAgentNickname(name string, nicknameResetCount int) string {
	if nicknameResetCount == 0 {
		return name
	}
	value := nicknameResetCount + 1
	suffix := "th"
	if value%100 < 11 || value%100 > 13 {
		switch value % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%s the %d%s", name, value, suffix)
}

type roundRobinChooser struct {
	next atomic.Uint64
}

func newRoundRobinChooser() *roundRobinChooser {
	return &roundRobinChooser{}
}

func (c *roundRobinChooser) Choose(values []string) string {
	if len(values) == 0 {
		return ""
	}
	index := c.next.Add(1) - 1
	return values[int(index%uint64(len(values)))]
}
